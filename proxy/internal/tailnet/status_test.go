package tailnet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leroux/torii-qubes/claims"
)

// A tag difference is reported in the field dom0's check reads.
func TestATagMismatchIsReportedForCheck(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_P8H7, "mismatch-named")
	if m := TagMismatch([]string{"tag:m1", "tag:m1-x"}, []string{"tag:m1-x", "tag:m1"}); m != "" {
		t.Errorf("same tags in another order: %q", m)
	}
	m := TagMismatch([]string{"tag:other"}, []string{"tag:m1-x", "tag:m1"})
	if !strings.Contains(m, "tag:other") {
		t.Fatalf("mismatch %q", m)
	}
	b, _ := json.Marshal(Status{TagMismatch: m})
	if !strings.Contains(string(b), `"tag_mismatch":"identity has tags [tag:other]`) {
		t.Errorf("status JSON %s", b)
	}
}
