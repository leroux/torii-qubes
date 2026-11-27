// Code generated from claims by charter gen. DO NOT EDIT.

// Package claims is the charter's record as Go: a symbol per entry and per
// observation, and the Verifies and Observes a test opens with.
package claims

import (
	"encoding/json"
	"os"
	"runtime"
	"sync"
	"testing"
)

// Status is an entry's status.
type Status string

// The statuses an entry may have.
const (
	Planned    Status = "planned"
	Active     Status = "active"
	Deprecated Status = "deprecated"
)

// Spec is an entry of the record: a need or a decision.
type Spec struct {
	ID, Title string
	Status    Status
	Kind      string
}

// Observation is a fact about a system the code does not own.
type Observation struct {
	Name, Subject, Status string
}

// Verifies opens a test that verifies an entry, naming by handle the
// scenarios it exercises. With CHARTER_EVENTS set, the test's outcome is
// recorded for each citation once it ends.
func Verifies(tb testing.TB, spec *Spec, handles ...string) {
	tb.Helper()
	if spec == nil {
		tb.Fatal("claims: Verifies needs a spec")
		return
	}
	record(tb, spec.ID, handles)
}

// Observes opens a probe that checks an observation against the live
// system.
func Observes(tb testing.TB, o *Observation) {
	tb.Helper()
	if o == nil {
		tb.Fatal("claims: Observes needs an observation")
		return
	}
	record(tb, o.Name, nil)
}

// event is one line of the events file.
type event struct {
	ID      string `json:"id"`
	Handle  string `json:"handle,omitempty"`
	Test    string `json:"test"`
	File    string `json:"file"`
	Outcome string `json:"outcome"`
}

var eventsMu sync.Mutex

func record(tb testing.TB, id string, handles []string) {
	path := os.Getenv("CHARTER_EVENTS")
	if path == "" {
		return
	}
	_, file, _, _ := runtime.Caller(2)
	if len(handles) == 0 {
		handles = []string{""}
	}
	tb.Cleanup(func() {
		outcome := "passed"
		switch {
		case tb.Failed():
			outcome = "failed"
		case tb.Skipped():
			outcome = "skipped"
		}
		eventsMu.Lock()
		defer eventsMu.Unlock()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			tb.Errorf("claims: the events file: %v", err)
			return
		}
		defer f.Close()
		for _, h := range handles {
			line, _ := json.Marshal(event{ID: id, Handle: h, Test: tb.Name(), File: file, Outcome: outcome})
			// One write per line, opened to append: a line is never split
			// by another process's.
			if _, err := f.Write(append(line, '\n')); err != nil {
				tb.Errorf("claims: the events file: %v", err)
			}
		}
	})
}

// STK_261126_346B is STK-261126-346B: A qube on an egress reaches the internet only through that egress's exit nodes, or not at all
var STK_261126_346B = &Spec{ID: "STK-261126-346B", Title: "A qube on an egress reaches the internet only through that egress's exit nodes, or not at all", Status: Planned}

// STK_261126_4Q75 is STK-261126-4Q75: show reads each qube's reach from the live machine; check names every difference from the file
var STK_261126_4Q75 = &Spec{ID: "STK-261126-4Q75", Title: "show reads each qube's reach from the live machine; check names every difference from the file", Status: Planned}

// STK_261126_B4CP is STK-261126-B4CP: apply changes only what torii owns, and exactly the steps plan showed
var STK_261126_B4CP = &Spec{ID: "STK-261126-B4CP", Title: "apply changes only what torii owns, and exactly the steps plan showed", Status: Planned}

// STK_261126_KHZB is STK-261126-KHZB: Each tailnet identity is one qube's, joins only by a person's approval, and dom0 holds no tailnet credential
var STK_261126_KHZB = &Spec{ID: "STK-261126-KHZB", Title: "Each tailnet identity is one qube's, joins only by a person's approval, and dom0 holds no tailnet credential", Status: Planned}

// STK_261126_KZK8 is STK-261126-KZK8: A carried qube's connection is relayed only if its entry allows it; every other connection is refused
var STK_261126_KZK8 = &Spec{ID: "STK-261126-KZK8", Title: "A carried qube's connection is relayed only if its entry allows it; every other connection is refused", Status: Planned}

// STK_261126_SM33 is STK-261126-SM33: torii adds no qrexec call, network path or dom0 exposure beyond those the file declares
var STK_261126_SM33 = &Spec{ID: "STK-261126-SM33", Title: "torii adds no qrexec call, network path or dom0 exposure beyond those the file declares", Status: Planned}
