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

func TestNodeMapperRejectsCollidingShortHost(t *testing.T) {
	// Two different nodes whose short hostnames collide: a wrong join is worse
	// than no join, so the ambiguous key must resolve to neither node.
	c := mockClient(t)
	c.Responses[pathVdcNodes] = `{"node":[
		{"nodename":"n1.dc1.example.com","mgmt_ip":"10.0.0.1","data_ip":"10.1.0.1"},
		{"nodename":"n1.dc2.example.com","mgmt_ip":"10.0.0.2","data_ip":"10.1.0.2"}
	]}`
	m, err := newNodeMapper(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := m.lookup("n1"); ok {
		t.Errorf("lookup(%q) = %q,true; want no match on the colliding short host", "n1", got)
	}
	if got, ok := m.lookup("n1.dc1.example.com"); !ok || got != "n1.dc1.example.com" {
		t.Errorf("lookup of full FQDN = %q,%v; want %q,true", got, ok, "n1.dc1.example.com")
	}
	if got, ok := m.lookup("n1.dc2.example.com"); !ok || got != "n1.dc2.example.com" {
		t.Errorf("lookup of full FQDN = %q,%v; want %q,true", got, ok, "n1.dc2.example.com")
	}
}

func TestNodeMapperResolvesFlatNetworkNode(t *testing.T) {
	// A flat-network node whose mgmt_ip equals its data_ip, and whose
	// unqualified nodename equals its own shortHost, re-registers the same key
	// under itself repeatedly. That must not be mistaken for a collision
	// between two different nodes.
	c := mockClient(t)
	c.Responses[pathVdcNodes] = `{"node":[
		{"nodename":"supr01-r01","mgmt_ip":"10.0.0.1","data_ip":"10.0.0.1"}
	]}`
	m, err := newNodeMapper(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"supr01-r01", "10.0.0.1"} {
		if got, ok := m.lookup(host); !ok || got != "supr01-r01" {
			t.Errorf("lookup(%q) = %q,%v; want %q,true", host, got, ok, "supr01-r01")
		}
	}
}
