package engine

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
)

// UDPReply sends datagrams back to the workload, from the address it
// sent to (the intercept layer binds a transparent socket there).
type UDPReply interface {
	Send(b []byte) error
	Close() error
}

// udpFlow is one (workload address, destination) pair: decided once,
// relayed until idle.
type udpFlow struct {
	workload string
	refused  bool
	up       net.Conn
	reply    UDPReply

	mu     sync.Mutex
	last   time.Time
	closed bool
}

func (f *udpFlow) touch(now time.Time) {
	f.mu.Lock()
	f.last = now
	f.mu.Unlock()
}

func (f *udpFlow) idle(now time.Time, d time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return now.Sub(f.last) > d
}

func (f *udpFlow) close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	f.mu.Unlock()
	if f.up != nil {
		f.up.Close()
	}
	if f.reply != nil {
		f.reply.Close()
	}
}

// HandleUDP takes one datagram from a routed workload at src, sent to
// dst. The first datagram of a flow is decided and logged; the rest
// follow the decision until the flow is idle. open makes the reply path,
// once, for an allowed flow.
func (e *Engine) HandleUDP(ctx context.Context, src, dst netip.AddrPort, b []byte, open func() (UDPReply, error)) {
	key := src.String() + ">" + dst.String()
	now := e.d.Clock.Now()
	e.mu.Lock()
	f := e.flows[key]
	if f != nil && f.idle(now, e.d.UDPIdle) {
		e.dropLocked(key, f)
		go f.close()
		f = nil
	}
	e.mu.Unlock()
	if f != nil {
		f.touch(now)
		if !f.refused {
			f.up.Write(b)
		}
		return
	}
	if name, _, ok := e.d.Config.ByAddress(src.Addr()); ok && e.atLimit(name, now) {
		return
	}
	f = e.newFlow(ctx, src, dst, open)
	f.touch(now)
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		f.close()
		return
	}
	if old := e.flows[key]; old != nil { // raced with another first datagram
		e.mu.Unlock()
		f.close()
		old.touch(now)
		if !old.refused {
			old.up.Write(b)
		}
		return
	}
	e.flows[key] = f
	e.nflows[f.workload]++
	e.mu.Unlock()
	if f.refused {
		return
	}
	f.up.Write(b)
	e.inc(f.workload, 1)
	e.wg.Add(1)
	go e.pumpUDP(key, f)
}

func (e *Engine) newFlow(ctx context.Context, src, dst netip.AddrPort, open func() (UDPReply, error)) *udpFlow {
	ev := events.Event{Direction: events.Out, Entry: policy.EntryIntercept, Network: "udp", Source: src.Addr().String(), Dest: dst.String()}
	name, w, ok := e.d.Config.ByAddress(src.Addr())
	if !ok {
		ev.Workload, ev.Verdict, ev.Reason = "-", policy.Refuse, policy.ReasonUnknownWorkload
		e.d.Log.Emit(ev)
		return &udpFlow{workload: "-", refused: true}
	}
	ev.Workload, ev.Source = name, name
	o := e.classify(ctx, policy.Outbound{Workload: name, Entry: policy.EntryIntercept, Port: dst.Port(), UDP: true}, dst.Addr().String())
	if !o.Tailnet {
		o.ResolvedFrom = e.cache.Names(name, o.Addr)
	}
	d := e.decide(o)
	fill(&ev, d)
	refused := &udpFlow{workload: name, refused: true}
	if d.Verdict != policy.Allow {
		e.d.Log.Emit(ev)
		return refused
	}
	dctx, cancel := context.WithTimeout(ctx, e.d.DialTimeout)
	defer cancel()
	var up net.Conn
	var err error
	switch d.Path {
	case policy.PathTailnet:
		up, err = e.d.Identities[name].DialUDP(dctx, dst)
	case policy.PathExit:
		up, err = e.dialExit(dctx, name, dst, true)
	default:
		up, err = e.d.Internet.DialContext(dctx, "udp", dst.String())
	}
	if err == nil && w.Link == config.LinkRouted {
		var reply UDPReply
		if reply, err = open(); err == nil {
			e.d.Log.Emit(ev)
			return &udpFlow{workload: name, up: up, reply: reply}
		}
		up.Close()
	}
	ev.Error = err.Error()
	e.d.Log.Emit(ev)
	return refused
}

// pumpUDP relays replies until the flow is idle or closed.
func (e *Engine) pumpUDP(key string, f *udpFlow) {
	defer e.wg.Done()
	defer e.inc(f.workload, -1)
	buf := make([]byte, 65535)
	for {
		f.up.SetReadDeadline(time.Now().Add(e.d.UDPIdle))
		n, err := f.up.Read(buf)
		if err != nil {
			var ne net.Error
			if isTimeout(err, &ne) && !f.idle(e.d.Clock.Now(), e.d.UDPIdle) {
				continue // the workload is still sending
			}
			break
		}
		f.touch(e.d.Clock.Now())
		if f.reply.Send(buf[:n]) != nil {
			break
		}
	}
	e.mu.Lock()
	if e.flows[key] == f {
		e.dropLocked(key, f)
	}
	e.mu.Unlock()
	f.close()
}

func (e *Engine) dropLocked(key string, f *udpFlow) {
	delete(e.flows, key)
	e.nflows[f.workload]--
}

// atLimit reports a workload at its flow cap, logging that at most once a
// second.
func (e *Engine) atLimit(workload string, now time.Time) bool {
	e.mu.Lock()
	over := e.nflows[workload] >= e.d.MaxUDPFlows
	logIt := over && now.Sub(e.limitLog[workload]) > time.Second
	if logIt {
		e.limitLog[workload] = now
	}
	e.mu.Unlock()
	if logIt {
		e.d.Log.Emit(events.Event{Workload: workload, Direction: events.Out, Entry: policy.EntryIntercept, Network: "udp",
			Source: workload, Verdict: policy.Refuse, Reason: policy.ReasonUDPFlowLimit})
	}
	return over
}

// sweepUDP drops idle refused flows, so a later datagram is decided again.
func (e *Engine) sweepUDP(ctx context.Context) {
	defer e.wg.Done()
	t := time.NewTicker(e.d.UDPIdle / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := e.d.Clock.Now()
		e.mu.Lock()
		for k, f := range e.flows {
			if f.refused && f.idle(now, e.d.UDPIdle) {
				e.dropLocked(k, f)
			}
		}
		e.mu.Unlock()
	}
}

func isTimeout(err error, ne *net.Error) bool {
	n, ok := err.(net.Error)
	if ok {
		*ne = n
	}
	return ok && n.Timeout()
}
