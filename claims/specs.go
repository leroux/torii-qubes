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

// STK_261126_346B is STK-261126-346B: torii MUST send an egress qube's internet traffic only through that egress's exit nodes, and MUST refuse it when none is healthy
var STK_261126_346B = &Spec{ID: "STK-261126-346B", Title: "torii MUST send an egress qube's internet traffic only through that egress's exit nodes, and MUST refuse it when none is healthy", Status: Planned}

// STK_261126_4Q75 is STK-261126-4Q75: torii show MUST report each qube's reach from the live machine, and torii check MUST name every difference from the file
var STK_261126_4Q75 = &Spec{ID: "STK-261126-4Q75", Title: "torii show MUST report each qube's reach from the live machine, and torii check MUST name every difference from the file", Status: Planned}

// STK_261126_B4CP is STK-261126-B4CP: torii apply MUST change only what torii owns, and MUST make exactly the steps torii plan showed
var STK_261126_B4CP = &Spec{ID: "STK-261126-B4CP", Title: "torii apply MUST change only what torii owns, and MUST make exactly the steps torii plan showed", Status: Planned}

// STK_261126_KHZB is STK-261126-KHZB: torii MUST give each tailnet identity to one qube, join it only with a person's approval, and keep no tailnet credential in dom0
var STK_261126_KHZB = &Spec{ID: "STK-261126-KHZB", Title: "torii MUST give each tailnet identity to one qube, join it only with a person's approval, and keep no tailnet credential in dom0", Status: Planned}

// STK_261126_KZK8 is STK-261126-KZK8: torii MUST relay a carried qube's connection only if the qube's entry allows it, and MUST refuse every other connection
var STK_261126_KZK8 = &Spec{ID: "STK-261126-KZK8", Title: "torii MUST relay a carried qube's connection only if the qube's entry allows it, and MUST refuse every other connection", Status: Planned}

// STK_261126_SM33 is STK-261126-SM33: torii MUST NOT add a qrexec call, network path or dom0 exposure beyond those the file declares
var STK_261126_SM33 = &Spec{ID: "STK-261126-SM33", Title: "torii MUST NOT add a qrexec call, network path or dom0 exposure beyond those the file declares", Status: Planned}

// SW_261127_2JH3 is SW-261127-2JH3: torii plan MUST refuse, before any step, a file that names what this machine cannot be given
var SW_261127_2JH3 = &Spec{ID: "SW-261127-2JH3", Title: "torii plan MUST refuse, before any step, a file that names what this machine cannot be given", Status: Active, Kind: "invariant"}

// SW_261127_2R9T is SW-261127-2R9T: The proxy MUST count a name entry only for a name the qube asked for, or resolved through the proxy to the address dialled
var SW_261127_2R9T = &Spec{ID: "SW-261127-2R9T", Title: "The proxy MUST count a name entry only for a name the qube asked for, or resolved through the proxy to the address dialled", Status: Active, Kind: "invariant"}

// SW_261127_2XS6 is SW-261127-2XS6: The proxy MUST match an allow entry by exact name or subdomain, port and protocol, treating an entry without a protocol as TCP only
var SW_261127_2XS6 = &Spec{ID: "SW-261127-2XS6", Title: "The proxy MUST match an allow entry by exact name or subdomain, port and protocol, treating an entry without a protocol as TCP only", Status: Active, Kind: "invariant"}

// SW_261127_3Q23 is SW-261127-3Q23: torii plan MUST turn IPv6 off on every proxy qube, for itself and every qube behind it
var SW_261127_3Q23 = &Spec{ID: "SW-261127-3Q23", Title: "torii plan MUST turn IPv6 off on every proxy qube, for itself and every qube behind it", Status: Active, Kind: "invariant"}

// SW_261127_3TPX is SW-261127-3TPX: An identity MUST sign in by a link a person approves, or by a one-time key that only the proxy qube keeps
var SW_261127_3TPX = &Spec{ID: "SW-261127-3TPX", Title: "An identity MUST sign in by a link a person approves, or by a one-time key that only the proxy qube keeps", Status: Active, Kind: "invariant"}

// SW_261127_62WV is SW-261127-62WV: show reports each qube's reach from the live machine, never from the file
var SW_261127_62WV = &Spec{ID: "SW-261127-62WV", Title: "show reports each qube's reach from the live machine, never from the file", Status: Active, Kind: "invariant"}

// SW_261127_63CS is SW-261127-63CS: torii apply MUST make plan's steps in order, stop at the first failure, and continue on the next run
var SW_261127_63CS = &Spec{ID: "SW-261127-63CS", Title: "torii apply MUST make plan's steps in order, stop at the first failure, and continue on the next run", Status: Active, Kind: "invariant"}

// SW_261127_6S2F is SW-261127-6S2F: The proxy MUST check an allowlisted connection's TLS server name before relaying any of the client's bytes
var SW_261127_6S2F = &Spec{ID: "SW-261127-6S2F", Title: "The proxy MUST check an allowlisted connection's TLS server name before relaying any of the client's bytes", Status: Active, Kind: "invariant"}

// SW_261127_6YGC is SW-261127-6YGC: The egress selector MUST NOT clear an exit node, and MUST switch in one change from the old node to the new
var SW_261127_6YGC = &Spec{ID: "SW-261127-6YGC", Title: "The egress selector MUST NOT clear an exit node, and MUST switch in one change from the old node to the new", Status: Active, Kind: "invariant"}

// SW_261127_7C3Y is SW-261127-7C3Y: The proxy MUST take a qrexec caller's identity from what dom0 sets, never from what the caller sends
var SW_261127_7C3Y = &Spec{ID: "SW-261127-7C3Y", Title: "The proxy MUST take a qrexec caller's identity from what dom0 sets, never from what the caller sends", Status: Active, Kind: "invariant"}

// SW_261127_8DFY is SW-261127-8DFY: The egress selector MUST judge an exit node healthy only while it is listed, answers pings, and is not failing every real connection
var SW_261127_8DFY = &Spec{ID: "SW-261127-8DFY", Title: "The egress selector MUST judge an exit node healthy only while it is listed, answers pings, and is not failing every real connection", Status: Active, Kind: "bound"}

// SW_261127_8PTP is SW-261127-8PTP: The proxy MUST NOT send Tailscale client logs unless the file asks, and MUST NOT map ports
var SW_261127_8PTP = &Spec{ID: "SW-261127-8PTP", Title: "The proxy MUST NOT send Tailscale client logs unless the file asks, and MUST NOT map ports", Status: Active, Kind: "invariant"}

// SW_261127_9BNW is SW-261127-9BNW: torii MUST read the machine file whole, and MUST refuse it whole when a key, a network or a value is wrong
var SW_261127_9BNW = &Spec{ID: "SW-261127-9BNW", Title: "torii MUST read the machine file whole, and MUST refuse it whole when a key, a network or a value is wrong", Status: Active, Kind: "invariant"}

// SW_261127_B6Z6 is SW-261127-B6Z6: The proxy qube MUST hand every routed qube's traffic to the program and MUST NOT forward any of it
var SW_261127_B6Z6 = &Spec{ID: "SW-261127-B6Z6", Title: "The proxy qube MUST hand every routed qube's traffic to the program and MUST NOT forward any of it", Status: Active, Kind: "invariant"}

// SW_261127_B8BM is SW-261127-B8BM: The status service is read-only: it reports, and changes nothing in the proxy
var SW_261127_B8BM = &Spec{ID: "SW-261127-B8BM", Title: "The status service is read-only: it reports, and changes nothing in the proxy", Status: Active, Kind: "invariant"}

// SW_261127_BHHX is SW-261127-BHHX: The proxy MUST identify a routed qube by its Qubes source address, which Qubes keeps it from forging
var SW_261127_BHHX = &Spec{ID: "SW-261127-BHHX", Title: "The proxy MUST identify a routed qube by its Qubes source address, which Qubes keeps it from forging", Status: Active, Kind: "invariant"}

// SW_261127_BNFC is SW-261127-BNFC: The egress selector MUST use exit nodes in the file's order, failing over at once and back only after a hold-down
var SW_261127_BNFC = &Spec{ID: "SW-261127-BNFC", Title: "The egress selector MUST use exit nodes in the file's order, failing over at once and back only after a hold-down", Status: Active, Kind: "invariant"}

// SW_261127_DH6R is SW-261127-DH6R: torii MUST only propose grants for the tailnet, and MUST NOT change the tailnet's policy
var SW_261127_DH6R = &Spec{ID: "SW-261127-DH6R", Title: "torii MUST only propose grants for the tailnet, and MUST NOT change the tailnet's policy", Status: Active, Kind: "invariant"}

// SW_261127_G26T is SW-261127-G26T: torii apply MUST change only what torii owns: its template, its proxy qubes, its policy files, and named settings of listed qubes
var SW_261127_G26T = &Spec{ID: "SW-261127-G26T", Title: "torii apply MUST change only what torii owns: its template, its proxy qubes, its policy files, and named settings of listed qubes", Status: Active, Kind: "invariant"}

// SW_261127_H3W9 is SW-261127-H3W9: The proxy's HTTP proxy MUST carry only one host and port per connection
var SW_261127_H3W9 = &Spec{ID: "SW-261127-H3W9", Title: "The proxy's HTTP proxy MUST carry only one host and port per connection", Status: Active, Kind: "invariant"}

// SW_261127_HJNX is SW-261127-HJNX: The proxy MUST refuse an egress's connections at once, and fail its DNS, while the egress has no healthy exit node
var SW_261127_HJNX = &Spec{ID: "SW-261127-HJNX", Title: "The proxy MUST refuse an egress's connections at once, and fail its DNS, while the egress has no healthy exit node", Status: Active, Kind: "invariant"}

// SW_261127_HSHY is SW-261127-HSHY: torii MUST install the proxy package from the dom0 package's bundle, with no repository for torii
var SW_261127_HSHY = &Spec{ID: "SW-261127-HSHY", Title: "torii MUST install the proxy package from the dom0 package's bundle, with no repository for torii", Status: Active, Kind: "invariant"}

// SW_261127_JWK2 is SW-261127-JWK2: The proxy template MUST load a guard at boot that keeps the proxy qube forwarding nothing, whatever the program does
var SW_261127_JWK2 = &Spec{ID: "SW-261127-JWK2", Title: "The proxy template MUST load a guard at boot that keeps the proxy qube forwarding nothing, whatever the program does", Status: Active, Kind: "invariant"}

// SW_261127_K5TK is SW-261127-K5TK: The proxy MUST dial an egress's traffic only into the egress identity's own network stack, never the system network
var SW_261127_K5TK = &Spec{ID: "SW-261127-K5TK", Title: "The proxy MUST dial an egress's traffic only into the egress identity's own network stack, never the system network", Status: Active, Kind: "invariant"}

// SW_261127_KWT2 is SW-261127-KWT2: A mesh's file MUST change only its own qubes and its own policy file
var SW_261127_KWT2 = &Spec{ID: "SW-261127-KWT2", Title: "A mesh's file MUST change only its own qubes and its own policy file", Status: Active, Kind: "invariant"}

// SW_261127_KYZW is SW-261127-KYZW: The proxy MUST send a qube's LAN traffic directly, never through its egress, and torii plan MUST say so
var SW_261127_KYZW = &Spec{ID: "SW-261127-KYZW", Title: "The proxy MUST send a qube's LAN traffic directly, never through its egress, and torii plan MUST say so", Status: Active, Kind: "invariant"}

// SW_261127_M36P is SW-261127-M36P: torii's qrexec policy MUST allow only calls that run in the direction of the connection they carry, and only those the file declares
var SW_261127_M36P = &Spec{ID: "SW-261127-M36P", Title: "torii's qrexec policy MUST allow only calls that run in the direction of the connection they carry, and only those the file declares", Status: Active, Kind: "invariant"}

// SW_261127_M7G9 is SW-261127-M7G9: The proxy MUST resolve a name, for the qube and for itself, only to addresses the qube may reach under that name
var SW_261127_M7G9 = &Spec{ID: "SW-261127-M7G9", Title: "The proxy MUST resolve a name, for the qube and for itself, only to addresses the qube may reach under that name", Status: Active, Kind: "invariant"}

// SW_261127_N3N8 is SW-261127-N3N8: torii-client MUST listen on loopback only and MUST make one qrexec call per connection
var SW_261127_N3N8 = &Spec{ID: "SW-261127-N3N8", Title: "torii-client MUST listen on loopback only and MUST make one qrexec call per connection", Status: Active, Kind: "invariant"}

// SW_261127_P8H7 is SW-261127-P8H7: torii MUST give a tagged identity exactly the file's tags, prefixed by the machine, and torii check MUST name any other
var SW_261127_P8H7 = &Spec{ID: "SW-261127-P8H7", Title: "torii MUST give a tagged identity exactly the file's tags, prefixed by the machine, and torii check MUST name any other", Status: Active, Kind: "invariant"}

// SW_261127_PXX9 is SW-261127-PXX9: The proxy MUST decide a connection against the entry of the qube it came from, and only by a way that entry offers
var SW_261127_PXX9 = &Spec{ID: "SW-261127-PXX9", Title: "The proxy MUST decide a connection against the entry of the qube it came from, and only by a way that entry offers", Status: Active, Kind: "invariant"}

// SW_261127_QMVP is SW-261127-QMVP: torii apply MUST NOT go past a proxy's config until the proxy reports running it
var SW_261127_QMVP = &Spec{ID: "SW-261127-QMVP", Title: "torii apply MUST NOT go past a proxy's config until the proxy reports running it", Status: Active, Kind: "invariant"}

// SW_261127_QQYS is SW-261127-QQYS: The proxy MUST decide UDP once per flow and MUST cap each qube's open flows
var SW_261127_QQYS = &Spec{ID: "SW-261127-QQYS", Title: "The proxy MUST decide UDP once per flow and MUST cap each qube's open flows", Status: Active, Kind: "bound"}

// SW_261127_R4CD is SW-261127-R4CD: Every connection the proxy decides is logged with its verdict and the rule that made it
var SW_261127_R4CD = &Spec{ID: "SW-261127-R4CD", Title: "Every connection the proxy decides is logged with its verdict and the rule that made it", Status: Active, Kind: "invariant"}

// SW_261127_SMYC is SW-261127-SMYC: check names each difference it looks for between the live machine and the file, and fails on one
var SW_261127_SMYC = &Spec{ID: "SW-261127-SMYC", Title: "check names each difference it looks for between the live machine and the file, and fails on one", Status: Active, Kind: "invariant"}

// SW_261127_T7GG is SW-261127-T7GG: The proxy MUST relay an inbound connection to a qube only on a port its entry or a service lists
var SW_261127_T7GG = &Spec{ID: "SW-261127-T7GG", Title: "The proxy MUST relay an inbound connection to a qube only on a port its entry or a service lists", Status: Active, Kind: "invariant"}

// SW_261127_VPGK is SW-261127-VPGK: torii MUST record an adopted qube's netvm once, and MUST leave a qube that leaves the file with no netvm
var SW_261127_VPGK = &Spec{ID: "SW-261127-VPGK", Title: "torii MUST record an adopted qube's netvm once, and MUST leave a qube that leaves the file with no netvm", Status: Active, Kind: "invariant"}

// SW_261127_WNGN is SW-261127-WNGN: The proxy MUST run each identity as a tailnet device of its own, with its own name and state
var SW_261127_WNGN = &Spec{ID: "SW-261127-WNGN", Title: "The proxy MUST run each identity as a tailnet device of its own, with its own name and state", Status: Active, Kind: "invariant"}

// SW_261127_WNYB is SW-261127-WNYB: The proxy MUST place every address in exactly one zone and MUST NOT dial a forbidden address
var SW_261127_WNYB = &Spec{ID: "SW-261127-WNYB", Title: "The proxy MUST place every address in exactly one zone and MUST NOT dial a forbidden address", Status: Active, Kind: "invariant"}

// SW_261127_WSWG is SW-261127-WSWG: An egress's identity MUST carry only internet traffic, and torii grants MUST propose it nothing else
var SW_261127_WSWG = &Spec{ID: "SW-261127-WSWG", Title: "An egress's identity MUST carry only internet traffic, and torii grants MUST propose it nothing else", Status: Active, Kind: "invariant"}

// SW_261127_XGDY is SW-261127-XGDY: A proxy MUST take its config whole or not at all, and a config that fails a check MUST NOT replace the running one
var SW_261127_XGDY = &Spec{ID: "SW-261127-XGDY", Title: "A proxy MUST take its config whole or not at all, and a config that fails a check MUST NOT replace the running one", Status: Active, Kind: "invariant"}

// SW_261127_Z5ST is SW-261127-Z5ST: explain gives the decision the proxy would make, without sending anything
var SW_261127_Z5ST = &Spec{ID: "SW-261127-Z5ST", Title: "explain gives the decision the proxy would make, without sending anything", Status: Active, Kind: "invariant"}

// QubesAntispoof is an observation of qubes: A Qubes netvm drops packets arriving on a qube's `vif` interface whose source address is not that qube's own; a qube cannot send as another.
var QubesAntispoof = &Observation{Name: "QubesAntispoof", Subject: "qubes", Status: "unsure"}

// QubesConnectTCPDenied is an observation of qubes: Qubes' own policy denies `qubes.ConnectTCP` between qubes unless a policy file allows the call.
var QubesConnectTCPDenied = &Observation{Name: "QubesConnectTCPDenied", Subject: "qubes", Status: "unsure"}
