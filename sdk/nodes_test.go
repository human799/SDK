package sdk

import (
	"testing"
)

func TestSelectFixedNodeSetStable(t *testing.T) {
	g := NodeGroups{A: []string{"a1:1", "a2:1"}, B: []string{"b1:1"}, C: []string{"c1:1"}, D: []string{"d1:1"}, E: []string{"e1:1"}}
	s1 := SelectFixedNodeSet("uuid-1", g)
	s2 := SelectFixedNodeSet("uuid-1", g)
	if s1 != s2 {
		t.Fatalf("fixed set not stable")
	}
}

func TestStableFallbackOrderWithRecentSuccess(t *testing.T) {
	set := FixedNodeSet{A: "a1:1", B: "b1:1", C: "c1:1"}
	// Test that recent success node comes first
	fallback := StableFallbackOrderWithRecentSuccess("uuid-1", set, "a1:1", "b1:1")
	if len(fallback) != 2 {
		t.Fatalf("expected 2 fallback nodes, got %d", len(fallback))
	}
	if fallback[0] != "b1:1" {
		t.Fatalf("expected recent success node first, got %s", fallback[0])
	}
}

