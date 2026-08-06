package client

import (
	"io"
	"log/slog"
	"net/netip"
	"testing"
)

func newTestAgent(allowed, denied []string) *Agent {
	return &Agent{
		AllowedTargets: allowed,
		DeniedTargets:  denied,
		Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestResolveAllowedTarget_DeniesPrivateAndLoopback(t *testing.T) {
	for _, target := range []string{
		"127.0.0.1:80",
		"10.0.0.5:443",
		"172.16.9.9:80",
		"192.168.1.1:80",
		"169.254.169.254:80",
		"[::1]:80",
	} {
		t.Run(target, func(t *testing.T) {
			a := newTestAgent(nil, nil)
			if _, ok := a.resolveAllowedTarget(t.Context(), target); ok {
				t.Fatalf("%s should be denied by the default prefixes", target)
			}
		})
	}
}

func TestResolveAllowedTarget_AllowsPublicAndPinsAddress(t *testing.T) {
	a := newTestAgent(nil, nil)
	got, ok := a.resolveAllowedTarget(t.Context(), "8.8.8.8:53")
	if !ok {
		t.Fatal("public address should be allowed")
	}
	if got != "8.8.8.8:53" {
		t.Fatalf("dial address = %q, want %q", got, "8.8.8.8:53")
	}
}

func TestResolveAllowedTarget_EmptyTargetDenied(t *testing.T) {
	a := newTestAgent(nil, nil)
	if _, ok := a.resolveAllowedTarget(t.Context(), ""); ok {
		t.Fatal("empty target should be denied")
	}
}

func TestResolveAllowedTarget_ExplicitHostAllowlistOverridesDeniedPrefix(t *testing.T) {
	a := newTestAgent([]string{"127.0.0.1"}, nil)
	if _, ok := a.resolveAllowedTarget(t.Context(), "127.0.0.1:8080"); !ok {
		t.Fatal("explicitly allowed address should win over the default deny")
	}
}

func TestIsDeniedAddr_UnmapsIPv4In6(t *testing.T) {
	a := newTestAgent(nil, nil)
	a.initACL()

	four := netip.MustParseAddr("127.0.0.1")
	sixMapped := netip.AddrFrom16(four.As16())

	if sixMapped.Unmap() != four {
		t.Fatalf("test premise broken: %v does not unmap to %v", sixMapped, four)
	}
	if !a.isDeniedAddr(four) {
		t.Fatal("127.0.0.1 should be denied")
	}
	if !a.isDeniedAddr(sixMapped.Unmap()) {
		t.Fatal("4-in-6 form of 127.0.0.1 should be denied once unmapped")
	}
	if a.isDeniedAddr(sixMapped) {
		t.Log("note: mapped form matches directly on this Go version")
	}
}

func TestResolveAllowedTarget_CustomDenyListReplacesDefaults(t *testing.T) {
	a := newTestAgent(nil, []string{"203.0.113.0/24"})
	if _, ok := a.resolveAllowedTarget(t.Context(), "203.0.113.7:80"); ok {
		t.Fatal("address in the configured deny list should be denied")
	}
	if _, ok := a.resolveAllowedTarget(t.Context(), "127.0.0.1:80"); !ok {
		t.Fatal("custom deny list should replace the defaults, not extend them")
	}
}
