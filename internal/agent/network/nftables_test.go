package network

import (
	"reflect"
	"testing"

	"github.com/google/nftables/expr"
)

func TestVMCChainName(t *testing.T) {
	if got := vmChainName("abc-123"); got != "vps_vm_abc-123" {
		t.Errorf("vmChainName: got %s", got)
	}
}

func TestParseMAC(t *testing.T) {
	b, err := parseMAC("02:00:00:00:00:01")
	if err != nil {
		t.Fatalf("parseMAC: %v", err)
	}
	want := []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	if !reflect.DeepEqual(b, want) {
		t.Errorf("mac wrong: %v", b)
	}
	if _, err := parseMAC("not-a-mac"); err == nil {
		t.Error("expected invalid mac to fail")
	}
}

func TestParseIP(t *testing.T) {
	v4, err := parseIP("192.0.2.10", false)
	if err != nil {
		t.Fatalf("parse ipv4: %v", err)
	}
	if !reflect.DeepEqual(v4, []byte{192, 0, 2, 10}) {
		t.Errorf("ipv4 wrong: %v", v4)
	}
	v6, err := parseIP("2001:db8::10", true)
	if err != nil {
		t.Fatalf("parse ipv6: %v", err)
	}
	if len(v6) != 16 || v6[15] != 0x10 {
		t.Errorf("ipv6 wrong: %v", v6)
	}
	// An IPv4-mapped address is rejected for the IPv6 field.
	if _, err := parseIP("192.0.2.10", true); err == nil {
		t.Error("expected ipv4 to fail for ipv6 field")
	}
}

// vmBindingRule builds the anti-spoofing expression sequences. Verify the
// structure without needing root access.
func TestVMBindingRuleIPv4(t *testing.T) {
	mac := []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	ip := []byte{192, 0, 2, 10}

	// MAC first: ether saddr == mac, ip saddr != ip, drop.
	rules := vmBindingRule(mac, ip, false, true)
	if len(rules) != 5 {
		t.Fatalf("expected 5 expressions, got %d", len(rules))
	}
	payload1, ok := rules[0].(*expr.Payload)
	if !ok || payload1.Base != expr.PayloadBaseLLHeader || payload1.Offset != 6 || payload1.Len != 6 {
		t.Errorf("first payload (ether saddr) wrong: %+v", rules[0])
	}
	cmp1, ok := rules[1].(*expr.Cmp)
	if !ok || cmp1.Op != expr.CmpOpEq || !reflect.DeepEqual(cmp1.Data, mac) {
		t.Errorf("first cmp (mac eq) wrong: %+v", rules[1])
	}
	payload2, ok := rules[2].(*expr.Payload)
	if !ok || payload2.Base != expr.PayloadBaseNetworkHeader || payload2.Offset != 12 || payload2.Len != 4 {
		t.Errorf("second payload (ip saddr) wrong: %+v", rules[2])
	}
	cmp2, ok := rules[3].(*expr.Cmp)
	if !ok || cmp2.Op != expr.CmpOpNeq || !reflect.DeepEqual(cmp2.Data, ip) {
		t.Errorf("second cmp (ip neq) wrong: %+v", rules[3])
	}
	verdict, ok := rules[4].(*expr.Verdict)
	if !ok || verdict.Kind != expr.VerdictDrop {
		t.Errorf("verdict wrong: %+v", rules[4])
	}

	// IP first: ip saddr == ip, ether saddr != mac, drop.
	rules2 := vmBindingRule(mac, ip, false, false)
	payload, ok := rules2[0].(*expr.Payload)
	if !ok || payload.Offset != 12 || payload.Len != 4 {
		t.Errorf("first payload should be ip saddr: %+v", rules2[0])
	}
	if !reflect.DeepEqual(rules2[2].(*expr.Payload), &expr.Payload{DestRegister: 1, Base: expr.PayloadBaseLLHeader, Offset: 6, Len: 6}) {
		t.Errorf("second payload should be ether saddr: %+v", rules2[2])
	}
}

func TestVMBindingRuleIPv6(t *testing.T) {
	mac := []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	ip := make([]byte, 16)
	ip[15] = 0x10 // 2001:db8::10

	rules := vmBindingRule(mac, ip, true, true)
	payload, ok := rules[2].(*expr.Payload)
	if !ok || payload.Base != expr.PayloadBaseNetworkHeader || payload.Offset != 8 || payload.Len != 16 {
		t.Errorf("ip6 saddr payload wrong: %+v", rules[2])
	}
	cmp, ok := rules[3].(*expr.Cmp)
	if !ok || cmp.Op != expr.CmpOpNeq || !reflect.DeepEqual(cmp.Data, ip) {
		t.Errorf("ip6 neq cmp wrong: %+v", rules[3])
	}
}

func TestPlatformConstants(t *testing.T) {
	if platformChain != "vps_platform" {
		t.Errorf("platform chain must be vps_platform")
	}
}
