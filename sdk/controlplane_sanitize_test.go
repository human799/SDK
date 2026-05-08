package sdk

import "testing"

func TestSanitizeNodeGroups(t *testing.T) {
	in := NodeGroups{
		A: []string{"1.1.1.1:443", "", "bad", "1.1.1.1:443"},
		B: nil, C: []string{},
		D: []string{"2.2.2.2:8443"},
		E: []string{"3.3.3.3:443", "3.3.3.3:443"},
	}
	got := sanitizeNodeGroups(in)
	if len(got.A) != 1 || got.A[0] != "1.1.1.1:443" {
		t.Fatalf("unexpected A")
	}
	if got.B != nil || got.C != nil {
		t.Fatalf("expected nil B/C")
	}
	if len(got.D) != 1 || len(got.E) != 1 {
		t.Fatalf("unexpected D/E")
	}
}

