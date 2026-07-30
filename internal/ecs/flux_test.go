package ecs

import (
	"testing"
)

func TestNodeMapperResolvesFluxHosts(t *testing.T) {
	// The vdc-nodes fixture names supr01-r01 at 10.0.0.1 / 10.1.0.1. Flux reports
	// host as an FQDN in the reference's example, so the mapper must join a
	// qualified name onto the bare nodename every other collector labels with.
	m, err := newNodeMapper(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ host, want string }{
		{"supr01-r01", "supr01-r01"},
		{"supr01-r01.example.com", "supr01-r01"},
		{"SUPR01-R01.EXAMPLE.COM", "supr01-r01"},
		{"10.0.0.1", "supr01-r01"},
		{"10.1.0.1", "supr01-r01"},
	} {
		got, ok := m.lookup(tc.host)
		if !ok || got != tc.want {
			t.Errorf("lookup(%q) = %q,%v; want %q,true", tc.host, got, ok, tc.want)
		}
	}
}

func TestNodeMapperRejectsUnknownHosts(t *testing.T) {
	// A host that joins nothing must fail loudly to its caller rather than
	// produce a series no dashboard query can line up with the rest.
	m, err := newNodeMapper(t.Context(), mockClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.lookup("someone-elses-node.example.com"); ok {
		t.Errorf("lookup of an unknown host returned %q, want no match", got)
	}
}

func TestShortHostLeavesIPsAlone(t *testing.T) {
	// Truncating an IPv4 address at its first dot produces a meaningless key
	// that could collide across nodes.
	if got := shortHost("10.0.0.1"); got != "10.0.0.1" {
		t.Errorf("shortHost(IP) = %q, want it unchanged", got)
	}
	if got := shortHost("n1.example.com"); got != "n1" {
		t.Errorf("shortHost(FQDN) = %q, want n1", got)
	}
}
