package sdk

import (
	"fmt"
	"hash/fnv"
	"sort"
)

type NodeGroups struct {
	A []string `json:"nodesA"`
	B []string `json:"nodesB"`
	C []string `json:"nodesC"`
	D []string `json:"nodesD"`
	E []string `json:"nodesE"`
}

type FixedNodeSet struct {
	A string `json:"A,omitempty"`
	B string `json:"B,omitempty"`
	C string `json:"C,omitempty"`
	D string `json:"D,omitempty"`
	E string `json:"E,omitempty"`
}

func SelectFixedNodeSet(uuid string, groups NodeGroups) FixedNodeSet {
	return FixedNodeSet{
		A: pickByUUID(uuid, "A", groups.A),
		B: pickByUUID(uuid, "B", groups.B),
		C: pickByUUID(uuid, "C", groups.C),
		D: pickByUUID(uuid, "D", groups.D),
		E: pickByUUID(uuid, "E", groups.E),
	}
}

func (s FixedNodeSet) AsList() []string {
	out := make([]string, 0, 5)
	for _, n := range []string{s.A, s.B, s.C, s.D, s.E} {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

func StableFallbackOrder(uuid string, set FixedNodeSet, current string) []string {
	nodes := set.AsList()
	type rank struct {
		node string
		v    uint64
	}
	arr := make([]rank, 0, len(nodes))
	for _, n := range nodes {
		if n == current {
			continue
		}
		arr = append(arr, rank{node: n, v: hash64(fmt.Sprintf("%s|fallback|%s", uuid, n))})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].v < arr[j].v })
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		out = append(out, it.node)
	}
	return out
}

func pickByUUID(uuid, partition string, nodes []string) string {
	if len(nodes) == 0 {
		return ""
	}
	idx := int(hash64(uuid+"|"+partition) % uint64(len(nodes)))
	return nodes[idx]
}

func hash64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

