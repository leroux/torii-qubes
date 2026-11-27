// Package dnsx answers the DNS questions of routed workloads and
// remembers, per workload, which names resolved to which addresses, so
// a later connection to an address can be decided by the name the
// workload asked for.
package dnsx

import (
	"errors"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Query is one parsed question.
type Query struct {
	Msg  dnsmessage.Message
	Name string // lowercased, no trailing dot
	Type dnsmessage.Type
}

// ErrMalformed: not a single-question DNS query.
var ErrMalformed = errors.New("dnsx: malformed query")

// Parse reads a query.
func Parse(b []byte) (Query, error) {
	var m dnsmessage.Message
	if err := m.Unpack(b); err != nil {
		return Query{}, ErrMalformed
	}
	if m.Header.Response || len(m.Questions) != 1 || m.Header.OpCode != 0 {
		return Query{Msg: m}, ErrMalformed
	}
	q := m.Questions[0]
	return Query{
		Msg:  m,
		Name: strings.TrimSuffix(strings.ToLower(q.Name.String()), "."),
		Type: q.Type,
	}, nil
}

func reply(q Query, rcode dnsmessage.RCode) dnsmessage.Message {
	return dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:                 q.Msg.Header.ID,
			Response:           true,
			RecursionDesired:   q.Msg.Header.RecursionDesired,
			RecursionAvailable: true,
			RCode:              rcode,
		},
		Questions: q.Msg.Questions,
	}
}

// Answer builds a reply carrying the addresses of the question's type
// (A or AAAA); for any other type, an empty NOERROR reply.
func Answer(q Query, addrs []netip.Addr, ttl time.Duration) ([]byte, error) {
	m := reply(q, dnsmessage.RCodeSuccess)
	secs := uint32(ttl / time.Second)
	for _, a := range addrs {
		a = a.Unmap()
		h := dnsmessage.ResourceHeader{Name: q.Msg.Questions[0].Name, Class: dnsmessage.ClassINET, TTL: secs}
		switch {
		case q.Type == dnsmessage.TypeA && a.Is4():
			h.Type = dnsmessage.TypeA
			m.Answers = append(m.Answers, dnsmessage.Resource{Header: h, Body: &dnsmessage.AResource{A: a.As4()}})
		case q.Type == dnsmessage.TypeAAAA && a.Is6():
			h.Type = dnsmessage.TypeAAAA
			m.Answers = append(m.Answers, dnsmessage.Resource{Header: h, Body: &dnsmessage.AAAAResource{AAAA: a.As16()}})
		}
	}
	return m.Pack()
}

// Error builds a reply with rcode and no answers.
func Error(q Query, rcode dnsmessage.RCode) ([]byte, error) {
	m := reply(q, rcode)
	return m.Pack()
}

// Clock is the time source; tests control it.
type Clock interface{ Now() time.Time }

// RealClock is time.Now.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// Cache remembers address-to-name mappings per workload until they expire.
//
//charter:realizes SW-261127-2R9T
type Cache struct {
	Clock Clock
	// MinTTL keeps a mapping at least this long, so a connection opened
	// just after a short-TTL answer still finds its name.
	MinTTL time.Duration

	mu sync.Mutex
	m  map[string]map[netip.Addr]map[string]time.Time
}

// Record notes that name resolved to addrs for workload.
func (c *Cache) Record(workload, name string, addrs []netip.Addr, ttl time.Duration) {
	if ttl < c.MinTTL {
		ttl = c.MinTTL
	}
	exp := c.Clock.Now().Add(ttl)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]map[netip.Addr]map[string]time.Time{}
	}
	w := c.m[workload]
	if w == nil {
		w = map[netip.Addr]map[string]time.Time{}
		c.m[workload] = w
	}
	for _, a := range addrs {
		a = a.Unmap()
		if w[a] == nil {
			w[a] = map[string]time.Time{}
		}
		if exp.After(w[a][name]) {
			w[a][name] = exp
		}
	}
}

// Names returns the unexpired names workload resolved to a, sorted.
func (c *Cache) Names(workload string, a netip.Addr) []string {
	now := c.Clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for n, exp := range c.m[workload][a.Unmap()] {
		if now.Before(exp) {
			out = append(out, n)
		} else {
			delete(c.m[workload][a.Unmap()], n)
		}
	}
	sort.Strings(out)
	return out
}
