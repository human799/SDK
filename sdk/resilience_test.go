package sdk

import (
	"testing"
	"time"
)

func TestExpBackoffSequence(t *testing.T) {
	base, max := time.Second, 60*time.Second
	got := []time.Duration{
		expBackoff(0, base, max), expBackoff(1, base, max), expBackoff(2, base, max),
		expBackoff(3, base, max), expBackoff(4, base, max), expBackoff(5, base, max), expBackoff(6, base, max),
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx=%d got=%v want=%v", i, got[i], want[i])
		}
	}
}

