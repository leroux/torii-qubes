package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
)

// httpCall opens torii.Connect+http as caller.
func (w *world) httpCall(caller string) (net.Conn, *bufio.Reader) {
	a, b := net.Pipe()
	go w.eng.HandleQrexec(context.Background(), caller, "http", b)
	a.SetDeadline(time.Now().Add(5 * time.Second))
	return a, bufio.NewReader(a)
}

func connect(t *testing.T, w *world, caller, target string) (net.Conn, *bufio.Reader, int) {
	t.Helper()
	c, br := w.httpCall(caller)
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("CONNECT %s: %v", target, err)
	}
	return c, br, resp.StatusCode
}

func TestHTTPConnectAllowlistAndTailnet(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	w.internet.serve(t, "151.101.0.223:443", "pypi")
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.name("evil.com", "6.6.6.6")
	serveFake(t, w.net.Node("store"), 8000, "store")

	c, br, code := connect(t, w, "job", "pypi.org:443")
	if code != 200 {
		t.Fatalf("pypi: %d", code)
	}
	if l, _ := br.ReadString('\n'); l != "pypi\n" {
		t.Errorf("got %q", l)
	}
	c.Close()

	c, br, code = connect(t, w, "job", "store:8000")
	if code != 200 {
		t.Fatalf("store: %d", code)
	}
	if l, _ := br.ReadString('\n'); l != "store\n" {
		t.Errorf("got %q", l)
	}
	c.Close()

	c, _, code = connect(t, w, "job", "evil.com:443")
	c.Close()
	if code != http.StatusForbidden {
		t.Errorf("evil.com: %d", code)
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool {
		return e.Entry == policy.EntryHTTP && strings.HasPrefix(e.Dest, "evil.com") && e.Reason == policy.ReasonNotAllowlisted
	})
	if w.internet.dials("6.6.6.6:443") != 0 {
		t.Error("refused destination was dialled")
	}

	// A qube that did not declare the HTTP proxy cannot use it.
	c, _, code = connect(t, w, "airgap", "store:8000")
	c.Close()
	if code != http.StatusForbidden {
		t.Errorf("airgap: %d", code)
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Workload == "airgap" && e.Reason == policy.ReasonNoHTTP })
}

// The plain-HTTP origin answers with the Host it was sent.
func serveHTTP(t *testing.T, w *world, addr string) {
	w.internet.serveRaw(t, addr, func(c net.Conn) {
		br := bufio.NewReader(c)
		for {
			req, err := http.ReadRequest(br)
			if err != nil {
				c.Close()
				return
			}
			body := "host=" + req.Host + " path=" + req.URL.Path
			fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		}
	})
}

func readBody(t *testing.T, br *bufio.Reader) (int, string) {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestHTTPPlainRequestsStayOnTheirHost(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	w.internet.name("pypi.org", "151.101.0.223")
	w.internet.name("evil.com", "151.101.0.223") // shares pypi's address, like a CDN
	serveHTTP(t, w, "151.101.0.223:80")

	c, br := w.httpCall("job")
	defer c.Close()
	// The Host header lies; the URL's host is what is decided and sent.
	fmt.Fprint(c, "GET http://pypi.org/simple/ HTTP/1.1\r\nHost: evil.com\r\nProxy-Connection: keep-alive\r\n\r\n")
	code, body := readBody(t, br)
	if code != 200 || body != "host=pypi.org path=/simple/" {
		t.Fatalf("%d %q", code, body)
	}
	// Keep-alive to the same host works.
	fmt.Fprint(c, "GET http://pypi.org/two HTTP/1.1\r\nHost: pypi.org\r\n\r\n")
	if code, body = readBody(t, br); code != 200 || body != "host=pypi.org path=/two" {
		t.Fatalf("%d %q", code, body)
	}
	// Another host on the same connection, even on the same address, ends it.
	fmt.Fprint(c, "GET http://evil.com/ HTTP/1.1\r\nHost: evil.com\r\n\r\n")
	if code, _ = readBody(t, br); code != http.StatusForbidden {
		t.Errorf("host change: %d", code)
	}
	waitEvent(t, w.eng.Log(), func(e events.Event) bool { return e.Reason == policy.ReasonHostChanged })

	// And on a fresh connection evil.com is refused by the allowlist.
	c2, br2 := w.httpCall("job")
	defer c2.Close()
	fmt.Fprint(c2, "GET http://evil.com/ HTTP/1.1\r\nHost: evil.com\r\n\r\n")
	if code, _ = readBody(t, br2); code != http.StatusForbidden {
		t.Errorf("evil.com: %d", code)
	}
}

func TestHTTPNeedsAbsoluteURLs(t *testing.T) {
	w := newWorld(t)
	w.start(t)
	c, br := w.httpCall("job")
	defer c.Close()
	fmt.Fprint(c, "GET /relative HTTP/1.1\r\nHost: pypi.org\r\n\r\n")
	if code, _ := readBody(t, br); code != http.StatusBadRequest {
		t.Errorf("%d", code)
	}
}
