package network

import (
	"reflect"
	"strings"
	"testing"
)

func TestVMChainName(t *testing.T) {
	if got := vmChain("abc-123"); got != "VPS_VM_abc-123" {
		t.Errorf("vmChain: got %s", got)
	}
}

func TestVMBindingRules(t *testing.T) {
	rules := vmBindingRules("VPS_VM_x", "02:00:00:00:00:01", []string{"192.0.2.10"})
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	// Rule 1: drop packets with VM MAC but non-assigned source IP.
	expected1 := []string{"-s", "!192.0.2.10", "-m", "mac", "--mac-source", "02:00:00:00:00:01", "-j", "DROP"}
	if rules[0].chain != "VPS_VM_x" || !reflect.DeepEqual(rules[0].spec, expected1) {
		t.Errorf("rule1 wrong: %+v", rules[0])
	}

	// Rule 2: drop packets with VM IP but from another MAC.
	expected2 := []string{"-s", "192.0.2.10", "-m", "mac", "!", "--mac-source", "02:00:00:00:00:01", "-j", "DROP"}
	if !reflect.DeepEqual(rules[1].spec, expected2) {
		t.Errorf("rule2 wrong: %+v", rules[1])
	}
}

func TestVMBindingRulesIPv6(t *testing.T) {
	rules := vmBindingRules("VPS_VM_y", "02:00:00:00:00:02", []string{"2001:db8::10"})
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if !strings.Contains(rules[0].spec[1], "2001:db8::10") {
		t.Errorf("ipv6 address not present: %+v", rules[0].spec)
	}
}

func TestVMBindingRulesMultipleIPs(t *testing.T) {
	rules := vmBindingRules("VPS_VM_z", "02:00:00:00:00:03", []string{"192.0.2.1", "192.0.2.2"})
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules for 2 IPs, got %d", len(rules))
	}
}

func TestPlatformChainConstant(t *testing.T) {
	if platformChain != "VPS_PLATFORM" {
		t.Errorf("platform chain must be VPS_PLATFORM")
	}
	if tableFilter != "filter" {
		t.Errorf("table must be filter")
	}
}
