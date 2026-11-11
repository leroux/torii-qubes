package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaia-PBC/lintel-qubes/proxy/internal/policy"
)

// go test ./internal/engine -run TestStress -stress 20 -v
var stressScale = flag.Float64("stress", 1, "scale of TestStress: connections and bytes")

// TestStress runs large transfers on every path at once while thousands of
// short connections (allowed, refused, closed before sending) open and
// close in between. It checks that every byte arrives intact both ways,
// that every connection leaves exactly one event with the verdict it
// should have, and that nothing is left behind: no active connection, no
// goroutine, no file descriptor.
func TestStress(t *testing.T) {
	s := *stressScale
	bulkPer, bulkSize := max(1, int(4*s)), 4<<20
	shorts, parallel := max(1, int(2000*s)), 128
	if testing.Short() {
		bulkPer, bulkSize, shorts = 1, 1<<20, 200
	}

	w := newWorld(t)
	w.eng.d.DialTimeout = 15 * time.Second // the daemon's: under load, loopback servers accept late
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.serveRaw(t, "151.101.0.223:443", func(c net.Conn) { io.Copy(c, c) })
	w.internet.serveRaw(t, "198.51.100.7:9000", func(c net.Conn) { io.Copy(c, c) })
	rawEcho(t, w.net.Node("store").Listen, 8000)
	w.qubes.serveRaw(t, "airgap", 8443, func(c net.Conn) { io.Copy(c, c) })
	w.start(t)

	evs, stop := w.eng.Log().Subscribe(1 << 20)
	defer stop()
	baseG, baseFD := runtime.NumGoroutine(), fds()

	// The ways in, each with what it is expected to log.
	type path struct {
		name    string
		dial    func() (net.Conn, error)
		halfTCP bool // the client side is TCP end to end: test half-close
	}
	web := netip.MustParseAddrPort("10.137.0.21:50000")
	paths := []path{
		{"socks-internet", func() (net.Conn, error) { return w.socksTCP("job", "pypi.org:443") }, true},
		{"socks-tailnet", func() (net.Conn, error) { return w.socksTCP("job", "store:8000") }, false},
		{"forward-tailnet", func() (net.Conn, error) { return w.qrexecTCP("job", "f.db") }, false},
		{"intercept-internet", func() (net.Conn, error) { return w.interceptTCP(web, "198.51.100.7:9000") }, true},
		{"inbound", func() (net.Conn, error) {
			return w.net.Node("laptop").Dial(context.Background(), netip.AddrPortFrom(w.net.Node("airgap").IP(), 443))
		}, false},
	}

	var allows, refuses, closes atomic.Int64
	var failures []string
	var fmu sync.Mutex
	fail := func(f string, a ...any) {
		fmu.Lock()
		if len(failures) < 20 {
			failures = append(failures, fmt.Sprintf(f, a...))
		}
		fmu.Unlock()
	}

	var wg sync.WaitGroup
	start := time.Now()
	var bulkBytes atomic.Int64

	// Large transfers: every path, bulkPer at once, bulkSize each way.
	for _, p := range paths {
		for i := range bulkPer {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, err := p.dial()
				if err != nil {
					fail("%s bulk %d: dial: %v", p.name, i, err)
					return
				}
				defer c.Close()
				allows.Add(1)
				if err := echoCheck(c, bulkSize, uint64(i), p.halfTCP); err != nil {
					fail("%s bulk %d: %v", p.name, i, err)
					return
				}
				bulkBytes.Add(int64(bulkSize))
			}()
		}
	}

	// In between: short connections of every kind.
	sem := make(chan struct{}, parallel)
	for i := range shorts {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			switch k := i % 8; {
			case k < 5: // allowed, a small echo
				p := paths[k]
				c, err := p.dial()
				if err != nil {
					fail("%s short %d: dial: %v", p.name, i, err)
					return
				}
				defer c.Close()
				allows.Add(1)
				if err := echoCheck(c, 1+i%3000, uint64(i), p.halfTCP && i%2 == 0); err != nil {
					fail("%s short %d: %v", p.name, i, err)
				}
			case k == 5: // refused: not on the allowlist
				refuses.Add(1)
				if c, err := w.socksTCP("job", "example.org:443"); err == nil {
					c.Close()
					fail("refused %d: socks connected", i)
				}
			case k == 6: // refused: a qube the proxy does not know
				refuses.Add(1)
				c, err := w.interceptTCP(netip.MustParseAddrPort("10.137.0.99:50000"), "198.51.100.7:9000")
				if err != nil {
					fail("unknown %d: %v", i, err)
					return
				}
				if n, _ := c.Read(make([]byte, 1)); n > 0 {
					fail("unknown %d: got data", i)
				}
				c.Close()
			case k == 7: // allowed, then closed before sending anything
				closes.Add(1)
				c, err := w.socksTCP("job", "pypi.org:443")
				if err != nil {
					fail("closed %d: %v", i, err)
					return
				}
				c.Close()
			}
		}()
	}
	wg.Wait()
	took := time.Since(start)
	for _, f := range failures {
		t.Error(f)
	}

	// Nothing left behind.
	var active int
	deadline := time.Now().Add(10 * time.Second)
	for {
		w.eng.mu.Lock()
		active = 0
		for _, n := range w.eng.active {
			active += n
		}
		w.eng.mu.Unlock()
		// Splice keeps pipes in a pool until a GC finalizes them; that
		// is the runtime's cache, not a leak.
		runtime.GC()
		g, fd := runtime.NumGoroutine(), fds()
		if active == 0 && g <= baseG+10 && (fd < 0 || fd <= baseFD+10) {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("left behind: %d active, goroutines %d (from %d), fds %d (from %d)", active, g, baseG, fd, baseFD)
			if os.Getenv("STRESS_DEBUG") != "" {
				out, _ := exec.Command("sh", "-c", "ls -l /proc/"+fmt.Sprint(os.Getpid())+"/fd | awk '{print $NF}' | sed 's/[0-9]*]$//' | sort | uniq -c").Output()
				t.Logf("%s", out)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Exactly one event per connection, with the right verdict.
	got := map[policy.Verdict]int64{}
	wait := time.After(5 * time.Second)
	want := allows.Load() + refuses.Load() + closes.Load()
	for total := int64(0); total < want; total++ {
		select {
		case e := <-evs:
			got[e.Verdict]++
			if e.Error != "" && e.Verdict == policy.Allow {
				t.Errorf("allowed with an error: %+v", e)
			}
		case <-wait:
			total = want
		}
	}
	time.Sleep(100 * time.Millisecond) // anything extra would arrive now
	for extra := true; extra; {
		select {
		case e := <-evs:
			got[e.Verdict]++
		default:
			extra = false
		}
	}
	if got[policy.Allow] != allows.Load() || got[policy.Refuse] != refuses.Load() || got[policy.Closed] != closes.Load() {
		t.Errorf("events: allow %d refuse %d closed %d; connections: allowed %d refused %d closed %d",
			got[policy.Allow], got[policy.Refuse], got[policy.Closed], allows.Load(), refuses.Load(), closes.Load())
	}
	t.Logf("%d connections (%d large of %d MiB) in %v; %.0f MiB/s each way across all large transfers; peak memory %s (test and fakes included)",
		want, len(paths)*bulkPer, bulkSize>>20, took.Round(time.Millisecond), float64(bulkBytes.Load())/(1<<20)/took.Seconds(), peakRSS())
}

// echoCheck sends n bytes of a stream seeded by seed and reads them back
// from an echo server while sending, comparing hashes. With half, it
// half-closes after sending and expects the echo to end with EOF;
// otherwise it reads exactly n bytes.
func echoCheck(c net.Conn, n int, seed uint64, half bool) error {
	c.SetDeadline(time.Now().Add(2 * time.Minute))
	r := rand.New(rand.NewPCG(seed, 7))
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(r.Uint32())
	}
	data[0] = 'B' // never a TLS record: the server name check lets it through
	sent := sha256.Sum256(data)
	werr := make(chan error, 1)
	go func() {
		_, err := c.Write(data)
		if err == nil && half {
			err = c.(*net.TCPConn).CloseWrite()
		}
		werr <- err
	}()
	h := sha256.New()
	var m int64
	var err error
	if half {
		m, err = io.Copy(h, c)
	} else {
		m, err = io.CopyN(h, c, int64(n))
	}
	if err != nil {
		return fmt.Errorf("read %d of %d: %v", m, n, err)
	}
	if e := <-werr; e != nil {
		return fmt.Errorf("write: %v", e)
	}
	if m != int64(n) || !bytes.Equal(h.Sum(nil), sent[:]) {
		return fmt.Errorf("got %d bytes back of %d, or different bytes", m, n)
	}
	return nil
}

// tcpPair is a connected pair of loopback TCP sockets.
func tcpPair() (client, server net.Conn, err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	defer ln.Close()
	acc := make(chan net.Conn, 1)
	go func() { c, _ := ln.Accept(); acc <- c }()
	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		return nil, nil, err
	}
	server = <-acc
	if server == nil {
		client.Close()
		return nil, nil, errors.New("accept failed")
	}
	return client, server, nil
}

// qrexecTCP is a qrexec call by caller with arg, over real TCP (so
// half-close and splice behave as in the daemon).
func (w *world) qrexecTCP(caller, arg string) (net.Conn, error) {
	c, s, err := tcpPair()
	if err != nil {
		return nil, err
	}
	go w.eng.HandleQrexec(context.Background(), caller, arg, s)
	return c, nil
}

// socksTCP connects through the SOCKS entry and returns the raw TCP
// connection (a SOCKS library's wrapper would hide CloseWrite).
func (w *world) socksTCP(caller, target string) (net.Conn, error) {
	c, err := w.qrexecTCP(caller, "socks")
	if err != nil {
		return nil, err
	}
	host, port, _ := net.SplitHostPort(target)
	var pn uint16
	fmt.Sscan(port, &pn)
	req := append([]byte{5, 1, 0, 5, 1, 0, 3, byte(len(host))}, host...)
	req = append(req, byte(pn>>8), byte(pn))
	c.SetDeadline(time.Now().Add(10 * time.Second))
	defer c.SetDeadline(time.Time{})
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	var rep [12]byte // method reply (2), then reply with an IPv4 address (10)
	if _, err := io.ReadFull(c, rep[:]); err != nil {
		c.Close()
		return nil, err
	}
	if rep[3] != 0 || rep[5] != 1 {
		c.Close()
		return nil, fmt.Errorf("socks reply %d", rep[3])
	}
	return c, nil
}

// interceptTCP is a routed qube at src connecting to dst, redirected.
func (w *world) interceptTCP(src netip.AddrPort, dst string) (net.Conn, error) {
	c, s, err := tcpPair()
	if err != nil {
		return nil, err
	}
	go w.eng.HandleIntercepted(context.Background(), s, src, netip.MustParseAddrPort(dst))
	return c, nil
}

func (q *fakeQubes) serveRaw(t *testing.T, qube string, port uint16, h func(net.Conn)) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go acceptLoop(ln, h)
	q.mu.Lock()
	q.servers[fmt.Sprintf("%s:%d", qube, port)] = ln.Addr().String()
	q.mu.Unlock()
}

func rawEcho(t *testing.T, listen func(uint16) (net.Listener, error), port uint16) {
	ln, err := listen(port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go acceptLoop(ln, func(c net.Conn) { io.Copy(c, c) })
}

// fds counts this process's open file descriptors, or -1 off Linux.
func fds() int {
	d, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(d)
}

// peakRSS is this process's peak resident memory, from /proc.
func peakRSS() string {
	b, _ := os.ReadFile("/proc/self/status")
	for _, l := range bytes.Split(b, []byte("\n")) {
		if bytes.HasPrefix(l, []byte("VmHWM:")) {
			return string(bytes.TrimSpace(l[6:]))
		}
	}
	return "?"
}
