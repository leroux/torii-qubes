package engine

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
)

// job3's tailnet_allow names store: store by any of its names or addresses,
// on any port; no other device, even one job3's grants would allow.
func TestTailnetAllowNamesADevice(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	store, other := w.net.Node("store"), w.net.Node("other")
	serveFake(t, store, 8000, "store")
	serveFake(t, store, 9000, "store-9000")
	serveFake(t, other, 8000, "other")
	for _, target := range []string{"store:8000", "store.fake.ts.net:8000", store.IP().String() + ":8000", "store:9000"} {
		c, err := w.socksDialAs("job3", target)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		c.Close()
	}
	for _, target := range []string{"other:8000", "other.fake.ts.net:8000", other.IP().String() + ":8000"} {
		if c, err := w.socksDialAs("job3", target); err == nil {
			c.Close()
			t.Errorf("%s reached", target)
		}
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool {
		return e.Workload == "job3" && e.Reason == policy.ReasonTailnetNotListed
	})
	// job, without a tailnet allowlist, still reaches other (the grants decide).
	if c, err := w.socksDialAs("job", "other:8000"); err != nil {
		t.Errorf("job without a list: %v", err)
	} else {
		c.Close()
	}
}

// The TLS server name must name a listed device: store's address with
// other's name is refused.
func TestTailnetAllowChecksTheServerName(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	store := w.net.Node("store")
	w.net.Node("other")
	ln, err := store.Listen(443)
	if err != nil {
		t.Fatal(err)
	}
	go acceptLoop(ln, func(c net.Conn) { buf := make([]byte, 512); c.Read(buf); c.Close() })
	c, br := w.httpCall("job3")
	defer c.Close()
	fmt.Fprintf(c, "CONNECT %s:443 HTTP/1.1\r\nHost: x\r\n\r\n", store.IP())
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT to store: %v %v", err, resp)
	}
	tc := tls.Client(&readerConn{Conn: c, r: br}, &tls.Config{ServerName: "other.fake.ts.net", InsecureSkipVerify: true})
	tc.SetDeadline(time.Now().Add(2 * time.Second))
	if err := tc.Handshake(); err == nil {
		t.Error("handshake went through with another device's name")
	}
	ev := waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "job3" && e.SNI != "" })
	if ev.Reason != policy.ReasonSNINotAllowed || ev.Verdict != policy.Refuse {
		t.Errorf("%+v", ev)
	}
	if !strings.Contains(ev.SNI, "other") {
		t.Errorf("sni %q", ev.SNI)
	}
}

// socksDialAs is socksDial for any caller (socksDial's name is fixed).
func (w *world) socksDialAs(caller, target string) (net.Conn, error) {
	c, err := w.socksDial(caller, target)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	_ = context.Background()
	return c, nil
}
