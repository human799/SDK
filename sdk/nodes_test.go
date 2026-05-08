package sdk

import "testing"

func TestSelectFixedNodeSetStable(t *testing.T) {
	g := NodeGroups{A: []string{"a1:1", "a2:1"}, B: []string{"b1:1"}, C: []string{"c1:1"}, D: []string{"d1:1"}, E: []string{"e1:1"}}
	s1 := SelectFixedNodeSet("uuid-1", g)
	s2 := SelectFixedNodeSet("uuid-1", g)
	if s1 != s2 {
		t.Fatalf("fixed set not stable")
	}
}

