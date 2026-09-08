// Package network implements VM network configuration: nftables IP/MAC binding
// to prevent a VM from using addresses that were not assigned to it.
//
// The implementation uses github.com/google/nftables (the iptables successor).
// Chain layout (idempotent, avoids rule explosion):
//
//	table vps  (family ip)     base chain vps_platform  hook forward prio 0
//	table vps6 (family ip6)    base chain vps_platform  hook forward prio 0
//	vps_platform:  jump vps_vm_<id>                     (one jump per VM)
//	vps_vm_<id>:
//	  ether saddr <mac> ip saddr != <ip>   drop          (VM MAC with foreign src)
//	  ip saddr <ip> ether saddr != <mac>   drop          (VM IP from a foreign MAC)
//
// All mutations are idempotent: re-provisioning a VM never duplicates rules.
package network

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
)

const (
	v4Table       = "vps"
	v6Table       = "vps6"
	platformChain = "vps_platform"
)

// Firewall is the VM firewall abstraction used by the agent.
type Firewall interface {
	// BindVMCreates installs IP/MAC anti-spoofing rules for a VM.
	BindVMCreates(vmID, mac string, ipv4Addrs, ipv6Addrs []string) error
	// DeleteVM removes all rules and chains for a VM.
	DeleteVM(vmID string) error
	// ListVMRules returns the rules installed for a VM (debugging/admin).
	ListVMRules(vmID string) ([]string, error)
}

// NftablesManager manages nftables rules for VMs.
type NftablesManager struct {
	mu   sync.Mutex
	conn *nftables.Conn
}

// NewNftablesManager creates a manager connected to the current network
// namespace's nftables and ensures the base tables/chains exist.
func NewNftablesManager() (*NftablesManager, error) {
	conn, err := nftables.New()
	if err != nil {
		return nil, fmt.Errorf("connect to nftables: %w", err)
	}
	m := &NftablesManager{conn: conn}
	if err := m.ensureBase(); err != nil {
		return nil, err
	}
	return m, nil
}

// ensureBase idempotently creates the platform tables and forward chains.
func (m *NftablesManager) ensureBase() error {
	for _, fam := range []nftables.TableFamily{nftables.TableFamilyIPv4, nftables.TableFamilyIPv6} {
		tableName := v4Table
		if fam == nftables.TableFamilyIPv6 {
			tableName = v6Table
		}
		m.conn.AddTable(&nftables.Table{Name: tableName, Family: fam})
		policy := nftables.ChainPolicyAccept
		m.conn.AddChain(&nftables.Chain{
			Name:     platformChain,
			Table:    &nftables.Table{Name: tableName, Family: fam},
			Hooknum:  nftables.ChainHookForward,
			Priority: nftables.ChainPriorityFilter,
			Type:     nftables.ChainTypeFilter,
			Policy:   &policy,
		})
	}
	return m.conn.Flush()
}

func (m *NftablesManager) table(fam nftables.TableFamily) *nftables.Table {
	name := v4Table
	if fam == nftables.TableFamilyIPv6 {
		name = v6Table
	}
	return &nftables.Table{Name: name, Family: fam}
}

// BindVMCreates installs the per-VM chain and IP/MAC binding rules for both
// address families. It is idempotent.
func (m *NftablesManager) BindVMCreates(vmID, mac string, ipv4Addrs, ipv6Addrs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	macBytes, err := parseMAC(mac)
	if err != nil {
		return err
	}

	// IPv4 table.
	if err := m.bindFamily(nftables.TableFamilyIPv4, vmID, macBytes, ipv4Addrs, false); err != nil {
		return err
	}
	// IPv6 table.
	if err := m.bindFamily(nftables.TableFamilyIPv6, vmID, macBytes, ipv6Addrs, true); err != nil {
		return err
	}
	return m.conn.Flush()
}

func (m *NftablesManager) bindFamily(fam nftables.TableFamily, vmID string, mac []byte, ips []string, ipv6 bool) error {
	table := m.table(fam)
	vmChain := vmChainName(vmID)
	platform := &nftables.Chain{Name: platformChain, Table: table}

	// Idempotency: drop any previous state for this VM first.
	if err := m.deleteVMInFamily(fam, vmID); err != nil {
		return err
	}

	// Create the regular VM chain.
	m.conn.AddChain(&nftables.Chain{Name: vmChain, Table: table})

	// Add the anti-spoofing rules.
	for _, ip := range ips {
		ipBytes, err := parseIP(ip, ipv6)
		if err != nil {
			return err
		}
		rule1 := vmBindingRule(mac, ipBytes, ipv6, true)  // mac matches, ip must not
		rule2 := vmBindingRule(mac, ipBytes, ipv6, false) // ip matches, mac must not
		m.conn.AddRule(&nftables.Rule{Table: table, Chain: &nftables.Chain{Name: vmChain, Table: table}, Exprs: rule1})
		m.conn.AddRule(&nftables.Rule{Table: table, Chain: &nftables.Chain{Name: vmChain, Table: table}, Exprs: rule2})
	}

	// Add the jump from the platform chain if not already present.
	exists, err := m.jumpExists(table, platform, vmChain)
	if err != nil {
		return err
	}
	if !exists {
		m.conn.AddRule(&nftables.Rule{
			Table: table,
			Chain: platform,
			Exprs: []expr.Any{&expr.Verdict{Kind: expr.VerdictJump, Chain: vmChain}},
		})
	}
	return nil
}

// vmBindingRule builds one anti-spoofing rule. When macFirst is true the rule
// matches the VM MAC then rejects a mismatched source IP; otherwise it matches
// the VM IP then rejects a mismatched MAC.
func vmBindingRule(mac, ip []byte, ipv6, macFirst bool) []expr.Any {
	ipOffset := uint32(12)
	ipLen := uint32(4)
	if ipv6 {
		ipOffset = 8
		ipLen = 16
	}
	macExpr := &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseLLHeader, Offset: 6, Len: 6}
	ipExpr := &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: ipOffset, Len: ipLen}

	rules := make([]expr.Any, 0, 5)
	if macFirst {
		rules = append(rules,
			macExpr,
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: mac},
			ipExpr,
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: ip},
		)
	} else {
		rules = append(rules,
			ipExpr,
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip},
			macExpr,
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: mac},
		)
	}
	rules = append(rules, &expr.Verdict{Kind: expr.VerdictDrop})
	return rules
}

// jumpExists reports whether the platform chain already jumps to vmChain.
func (m *NftablesManager) jumpExists(table *nftables.Table, platform *nftables.Chain, vmChain string) (bool, error) {
	rules, err := m.conn.GetRules(table, platform)
	if err != nil {
		return false, fmt.Errorf("list platform rules: %w", err)
	}
	for _, r := range rules {
		for _, e := range r.Exprs {
			if v, ok := e.(*expr.Verdict); ok && v.Kind == expr.VerdictJump && v.Chain == vmChain {
				return true, nil
			}
		}
	}
	return false, nil
}

// DeleteVM removes the per-VM chain and its jump from the platform chains.
func (m *NftablesManager) DeleteVM(vmID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.deleteVMInFamily(nftables.TableFamilyIPv4, vmID); err != nil {
		return err
	}
	if err := m.deleteVMInFamily(nftables.TableFamilyIPv6, vmID); err != nil {
		return err
	}
	return m.conn.Flush()
}

func (m *NftablesManager) deleteVMInFamily(fam nftables.TableFamily, vmID string) error {
	table := m.table(fam)
	vmChain := vmChainName(vmID)
	platform := &nftables.Chain{Name: platformChain, Table: table}

	// Remove the jump rule from the platform chain.
	rules, err := m.conn.GetRules(table, platform)
	if err == nil {
		for _, r := range rules {
			for _, e := range r.Exprs {
				if v, ok := e.(*expr.Verdict); ok && v.Kind == expr.VerdictJump && v.Chain == vmChain {
					if err := m.conn.DelRule(r); err != nil {
						return fmt.Errorf("delete jump rule: %w", err)
					}
				}
			}
		}
	}

	// Remove the VM chain if it exists.
	if _, err := m.conn.ListChain(table, vmChain); err == nil {
		m.conn.FlushChain(&nftables.Chain{Name: vmChain, Table: table})
		m.conn.DelChain(&nftables.Chain{Name: vmChain, Table: table})
	}
	return nil
}

// ListVMRules returns a human-readable dump of the VM chain rules.
func (m *NftablesManager) ListVMRules(vmID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vmChain := vmChainName(vmID)
	var out []string
	for _, fam := range []nftables.TableFamily{nftables.TableFamilyIPv4, nftables.TableFamilyIPv6} {
		table := m.table(fam)
		rules, err := m.conn.GetRules(table, &nftables.Chain{Name: vmChain, Table: table})
		if err != nil {
			continue
		}
		for _, r := range rules {
			out = append(out, fmt.Sprintf("%s/%s handle=%d exprs=%d", table.Name, vmChain, r.Handle, len(r.Exprs)))
		}
	}
	return out, nil
}

func vmChainName(vmID string) string {
	return "vps_vm_" + vmID
}

func parseMAC(s string) ([]byte, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return nil, fmt.Errorf("invalid mac address: %s", s)
	}
	out := make([]byte, 6)
	for i, p := range parts {
		var b byte
		if _, err := fmt.Sscanf(p, "%02x", &b); err != nil {
			return nil, fmt.Errorf("invalid mac address %s: %w", s, err)
		}
		out[i] = b
	}
	return out, nil
}

func parseIP(s string, ipv6 bool) ([]byte, error) {
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("invalid ip address: %s", s)
	}
	if ipv6 {
		// Reject IPv4 / IPv4-mapped addresses for the IPv6 field.
		if ip.To4() != nil {
			return nil, fmt.Errorf("expected ipv6 address, got %s", s)
		}
		return ip.To16(), nil
	}
	b := ip.To4()
	if b == nil {
		return nil, fmt.Errorf("expected ipv4 address, got %s", s)
	}
	return b, nil
}

var _ Firewall = (*NftablesManager)(nil)
