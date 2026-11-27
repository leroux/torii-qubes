package events

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leroux/torii-qubes/claims"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
)

// Each qube keeps its own last events, and every event goes to its file.
func TestTheLogKeepsEachQubesLastEvents(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_R4CD, "kept-per-qube")
	dir := t.TempDir()
	l := &Log{Dir: dir, Keep: 2}
	defer l.Close()
	for _, d := range []string{"a1", "a2", "a3"} {
		l.Emit(Event{Workload: "a", Dest: d, Verdict: policy.Refuse, Reason: policy.ReasonNotAllowlisted})
	}
	l.Emit(Event{Workload: "b", Dest: "b1", Verdict: policy.Allow, Reason: policy.ReasonAllowlisted})
	var a []string
	for _, e := range l.Recent("a") {
		a = append(a, e.Dest)
	}
	if strings.Join(a, " ") != "a2 a3" {
		t.Errorf("a keeps %v", a)
	}
	if b := l.Recent("b"); len(b) != 1 || b[0].Dest != "b1" || b[0].Time.IsZero() {
		t.Errorf("b keeps %+v; a's events pushed out b's, or the time is unset", b)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n"); n != 3 || !strings.Contains(string(raw), `"reason":"not-allowlisted"`) {
		t.Errorf("a's file holds %d events:\n%s", n, raw)
	}
}
