// Package network implements VM network configuration: iptables IP/MAC
// binding to prevent a VM from using addresses that were not assigned to it.
//
// Rule layout (idempotent, chain-based to avoid FORWARD chain explosion):
//
//	FORWARD   -j VPS_PLATFORM                 (created once)
//	VPS_PLATFORM -j VPS_VM_<id>               (one jump per VM)
//	VPS_VM_<id>:
//	  -s ! <ip> -m mac --mac-source <mac> -j DROP
//	  -s <ip>  -m mac ! --mac-source <mac> -j DROP
//
// All mutations check for existing rules before inserting, so re-provisioning
// a VM never duplicates rules.
package network

import (
	"fmt"

	"github.com/coreos/go-iptables/iptables"
)

const (
	platformChain = "VPS_PLATFORM"
	tableFilter   = "filter"
)

// IptablesManager manages IP/MAC binding rules for VMs.
type IptablesManager struct {
	ipv4 *iptables.IPTables
	ipv6 *iptables.IPTables
}

// NewIptablesManager creates an IPv4+IPv6 iptables manager.
func NewIptablesManager() (*IptablesManager, error) {
	v4, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)
	if err != nil {
		return nil, fmt.Errorf("init ipv4 iptables: %w", err)
	}
	v6, err := iptables.NewWithProtocol(iptables.ProtocolIPv6)
	if err != nil {
		return nil, fmt.Errorf("init ipv6 iptables: %w", err)
	}
	m := &IptablesManager{ipv4: v4, ipv6: v6}
	if err := m.ensureBase(); err != nil {
		return nil, err
	}
	return m, nil
}

// ensureBase creates the platform chain and the FORWARD jump once.
func (m *IptablesManager) ensureBase() error {
	for _, ipt := range []*iptables.IPTables{m.ipv4, m.ipv6} {
		if err := m.ensureChain(ipt, platformChain); err != nil {
			return err
		}
		if err := m.ensureForwardJump(ipt); err != nil {
			return err
		}
	}
	return nil
}

func (m *IptablesManager) ensureChain(ipt *iptables.IPTables, chain string) error {
	exists, err := ipt.ChainExists(tableFilter, chain)
	if err != nil {
		return fmt.Errorf("check chain %s: %w", chain, err)
	}
	if !exists {
		if err := ipt.NewChain(tableFilter, chain); err != nil {
			return fmt.Errorf("create chain %s: %w", chain, err)
		}
	}
	return nil
}

func (m *IptablesManager) ensureForwardJump(ipt *iptables.IPTables) error {
	rule := []string{"-j", platformChain}
	exists, err := ipt.Exists(tableFilter, "FORWARD", rule...)
	if err != nil {
		return fmt.Errorf("check forward jump: %w", err)
	}
	if !exists {
		if err := ipt.Append(tableFilter, "FORWARD", rule...); err != nil {
			return fmt.Errorf("append forward jump: %w", err)
		}
	}
	return nil
}

// BindVMCreates sets up the per-VM chain and binds MAC->IPs for both address
// families. It is idempotent.
func (m *IptablesManager) BindVMCreates(vmID, mac string, ipv4Addrs, ipv6Addrs []string) error {
	vmChain := vmChain(vmID)

	for _, ipt := range []*iptables.IPTables{m.ipv4, m.ipv6} {
		if err := m.ensureChain(ipt, vmChain); err != nil {
			return err
		}
	}

	if err := m.bindRules(m.ipv4, vmChain, mac, ipv4Addrs); err != nil {
		return err
	}
	if err := m.bindRules(m.ipv6, vmChain, mac, ipv6Addrs); err != nil {
		return err
	}

	// Hook VM chain into the platform chain.
	for _, ipt := range []*iptables.IPTables{m.ipv4, m.ipv6} {
		jump := []string{"-j", vmChain}
		exists, err := ipt.Exists(tableFilter, platformChain, jump...)
		if err != nil {
			return fmt.Errorf("check vm jump: %w", err)
		}
		if !exists {
			if err := ipt.Append(tableFilter, platformChain, jump...); err != nil {
				return fmt.Errorf("append vm jump %s: %w", vmChain, err)
			}
		}
	}
	return nil
}

// bindRules adds anti-spoofing rules for each allowed IP. It is idempotent.
func (m *IptablesManager) bindRules(ipt *iptables.IPTables, chain, mac string, ips []string) error {
	for _, rule := range vmBindingRules(chain, mac, ips) {
		if err := m.appendIfMissing(ipt, rule.chain, rule.spec); err != nil {
			return err
		}
	}
	return nil
}

// vmRule is a single iptables rule spec.
type vmRule struct {
	chain string
	spec  []string
}

// vmBindingRules returns the idempotent IP/MAC anti-spoofing rule specs for a
// VM chain. It is a pure function so the rules can be unit-tested without
// touching the kernel.
func vmBindingRules(chain, mac string, ips []string) []vmRule {
	var rules []vmRule
	for _, ip := range ips {
		// Drop traffic claiming the VM MAC but sourced from an unassigned IP.
		rules = append(rules, vmRule{
			chain: chain,
			spec:  []string{"-s", "!" + ip, "-m", "mac", "--mac-source", mac, "-j", "DROP"},
		})
		// Drop traffic sourced from the VM's IP but from another MAC.
		rules = append(rules, vmRule{
			chain: chain,
			spec:  []string{"-s", ip, "-m", "mac", "!", "--mac-source", mac, "-j", "DROP"},
		})
	}
	return rules
}

func (m *IptablesManager) appendIfMissing(ipt *iptables.IPTables, chain string, rule []string) error {
	exists, err := ipt.Exists(tableFilter, chain, rule...)
	if err != nil {
		return fmt.Errorf("check rule %v: %w", rule, err)
	}
	if !exists {
		if err := ipt.Append(tableFilter, chain, rule...); err != nil {
			return fmt.Errorf("append rule %v: %w", rule, err)
		}
	}
	return nil
}

// DeleteVM removes the per-VM chain and its jump from the platform chain.
func (m *IptablesManager) DeleteVM(vmID string) error {
	vmChain := vmChain(vmID)
	for _, ipt := range []*iptables.IPTables{m.ipv4, m.ipv6} {
		jump := []string{"-j", vmChain}
		// Delete the jump rule first, then flush and remove the chain.
		if exists, _ := ipt.Exists(tableFilter, platformChain, jump...); exists {
			if err := ipt.Delete(tableFilter, platformChain, jump...); err != nil {
				return fmt.Errorf("delete vm jump %s: %w", vmChain, err)
			}
		}
		if err := ipt.ClearAndDeleteChain(tableFilter, vmChain); err != nil {
			// Chain may not exist; that is fine.
			continue
		}
	}
	return nil
}

// ListVMRules returns the current rules for a VM chain (for debugging/admin).
func (m *IptablesManager) ListVMRules(vmID string) ([]string, error) {
	rules, err := m.ipv4.List(tableFilter, vmChain(vmID))
	if err != nil {
		return nil, fmt.Errorf("list vm rules: %w", err)
	}
	return rules, nil
}

func vmChain(vmID string) string {
	return fmt.Sprintf("VPS_VM_%s", vmID)
}
