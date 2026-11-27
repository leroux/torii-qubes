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

// SW_261127_2R9T is SW-261127-2R9T: A name entry counts only for a name the qube asked for, or resolved through the proxy to that address
var SW_261127_2R9T = &Spec{ID: "SW-261127-2R9T", Title: "A name entry counts only for a name the qube asked for, or resolved through the proxy to that address", Status: Active, Kind: "invariant"}

// SW_261127_2XS6 is SW-261127-2XS6: Allow entries match by exact name or subdomain, port and protocol, TCP unless they say UDP
var SW_261127_2XS6 = &Spec{ID: "SW-261127-2XS6", Title: "Allow entries match by exact name or subdomain, port and protocol, TCP unless they say UDP", Status: Active, Kind: "invariant"}

// SW_261127_6S2F is SW-261127-6S2F: Under an allowlist, a TLS server name must be allowed too, and nothing is relayed before it is read
var SW_261127_6S2F = &Spec{ID: "SW-261127-6S2F", Title: "Under an allowlist, a TLS server name must be allowed too, and nothing is relayed before it is read", Status: Active, Kind: "invariant"}

// SW_261127_H3W9 is SW-261127-H3W9: The HTTP proxy carries one host per connection
var SW_261127_H3W9 = &Spec{ID: "SW-261127-H3W9", Title: "The HTTP proxy carries one host per connection", Status: Active, Kind: "invariant"}

// SW_261127_M7G9 is SW-261127-M7G9: A name resolves, for the qube and for the proxy, only to addresses the qube may reach under it
var SW_261127_M7G9 = &Spec{ID: "SW-261127-M7G9", Title: "A name resolves, for the qube and for the proxy, only to addresses the qube may reach under it", Status: Active, Kind: "invariant"}

// SW_261127_PXX9 is SW-261127-PXX9: A connection is decided against the entry of the qube it came from, by the way it arrived
var SW_261127_PXX9 = &Spec{ID: "SW-261127-PXX9", Title: "A connection is decided against the entry of the qube it came from, by the way it arrived", Status: Active, Kind: "invariant"}

// SW_261127_QQYS is SW-261127-QQYS: UDP is decided per flow, and each qube has a cap on open flows
var SW_261127_QQYS = &Spec{ID: "SW-261127-QQYS", Title: "UDP is decided per flow, and each qube has a cap on open flows", Status: Active, Kind: "bound"}

// SW_261127_T7GG is SW-261127-T7GG: An inbound connection reaches a qube only on a port its entry or a service lists
var SW_261127_T7GG = &Spec{ID: "SW-261127-T7GG", Title: "An inbound connection reaches a qube only on a port its entry or a service lists", Status: Active, Kind: "invariant"}

// SW_261127_WNYB is SW-261127-WNYB: Every address is in exactly one zone, and a forbidden address is never dialled
var SW_261127_WNYB = &Spec{ID: "SW-261127-WNYB", Title: "Every address is in exactly one zone, and a forbidden address is never dialled", Status: Active, Kind: "invariant"}
