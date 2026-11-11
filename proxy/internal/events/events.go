// Package events is the decision log: one structured event per decision.
// Tests assert on it; show reads it, per workload.
package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gaia-PBC/torii-qubes/proxy/internal/policy"
)

// Direction of a connection relative to the workload.
type Direction string

const (
	Out Direction = "out"
	In  Direction = "in"
	DNS Direction = "dns"
	// Egress: an egress's own events (workload "egress:<name>").
	Egress Direction = "egress"
)

// Event is one decision.
type Event struct {
	Time      time.Time      `json:"time"`
	Workload  string         `json:"workload"`
	Direction Direction      `json:"direction"`
	Entry     policy.Entry   `json:"entry,omitempty"`
	Network   string         `json:"network,omitempty"` // "udp"; empty is TCP
	Source    string         `json:"source,omitempty"`  // who opened it: a qube, or a tailnet peer
	Dest      string         `json:"dest"`              // what was asked for
	SNI       string         `json:"sni,omitempty"`
	Verdict   policy.Verdict `json:"verdict"`
	Reason    policy.Reason  `json:"reason"`
	Path      policy.Path    `json:"path,omitempty"`
	Identity  string         `json:"identity,omitempty"`
	Host      string         `json:"host,omitempty"`
	// Error is set when an allowed connection could not be made.
	Error string `json:"error,omitempty"`
}

// Log keeps the last Keep events per workload in memory and, when Dir is
// set, appends each to <Dir>/<workload>.jsonl.
type Log struct {
	Dir  string
	Keep int
	Now  func() time.Time

	mu    sync.Mutex
	ring  map[string][]Event
	files map[string]*os.File
	subs  map[chan Event]struct{}
}

// Emit records e, filling Time.
func (l *Log) Emit(e Event) {
	if e.Time.IsZero() {
		if l.Now != nil {
			e.Time = l.Now()
		} else {
			e.Time = time.Now().UTC()
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ring == nil {
		l.ring = map[string][]Event{}
	}
	keep := l.Keep
	if keep == 0 {
		keep = 1000
	}
	r := append(l.ring[e.Workload], e)
	if len(r) > keep {
		r = r[len(r)-keep:]
	}
	l.ring[e.Workload] = r
	if l.Dir != "" {
		l.appendFile(e)
	}
	for ch := range l.subs {
		select {
		case ch <- e:
		default: // a slow subscriber loses events, never blocks traffic
		}
	}
}

func (l *Log) appendFile(e Event) {
	if l.files == nil {
		l.files = map[string]*os.File{}
	}
	f := l.files[e.Workload]
	if f == nil {
		var err error
		f, err = os.OpenFile(filepath.Join(l.Dir, e.Workload+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return
		}
		l.files[e.Workload] = f
	}
	b, _ := json.Marshal(e)
	f.Write(append(b, '\n'))
}

// Recent returns the remembered events of workload, or of all workloads
// when workload is "", oldest first within each workload.
func (l *Log) Recent(workload string) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if workload != "" {
		return append([]Event(nil), l.ring[workload]...)
	}
	var out []Event
	for _, r := range l.ring {
		out = append(out, r...)
	}
	return out
}

// Subscribe returns a channel of future events and a cancel func.
func (l *Log) Subscribe(buf int) (<-chan Event, func()) {
	ch := make(chan Event, buf)
	l.mu.Lock()
	if l.subs == nil {
		l.subs = map[chan Event]struct{}{}
	}
	l.subs[ch] = struct{}{}
	l.mu.Unlock()
	return ch, func() {
		l.mu.Lock()
		delete(l.subs, ch)
		l.mu.Unlock()
	}
}

// Close closes the log files.
func (l *Log) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, f := range l.files {
		f.Close()
	}
	l.files = nil
}
