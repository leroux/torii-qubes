package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/events"
	"github.com/leroux/torii-qubes/proxy/internal/policy"
)

// handleHTTP serves torii.Connect+http: an HTTP proxy, what most tools
// use through HTTPS_PROXY and HTTP_PROXY.
//
// CONNECT host:port is decided like a SOCKS request by name, then carried
// with the same server-name check. A plain request in absolute form
// (GET http://host/path) is decided by the URL's host; it and every later
// request on the connection are parsed, sent with Host set to that host,
// and a request for any other host:port ends the connection. So unlike
// raw TCP, plain HTTP through this proxy cannot reach another site on a
// shared address with a different Host header.
func (e *Engine) handleHTTP(ctx context.Context, workload string, c net.Conn) {
	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	req, err := http.ReadRequest(br)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		e.httpConnect(ctx, workload, c, br, req)
		return
	}
	e.httpForward(ctx, workload, c, br, req)
}

func httpError(c net.Conn, code int, why string) {
	body := why + "\n"
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}

func (e *Engine) httpConnect(ctx context.Context, workload string, c net.Conn, br *bufio.Reader, req *http.Request) {
	host, port, err := config.SplitHostPort(req.Host)
	if err != nil || host == "" || port == 0 {
		httpError(c, http.StatusBadRequest, "CONNECT needs host:port")
		return
	}
	o := e.classify(ctx, policy.Outbound{Workload: workload, Entry: policy.EntryHTTP, Port: port}, host)
	up, d, ev := e.decideAndDial(ctx, &o, req.Host)
	if up == nil {
		if d.Verdict == policy.Allow {
			httpError(c, http.StatusBadGateway, "unreachable")
		} else {
			httpError(c, http.StatusForbidden, "refused by torii: "+string(d.Reason))
		}
		return
	}
	defer up.Close()
	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	e.carry(ctx, o, ev, &readerConn{Conn: c, r: br}, up, true)
}

// hostPort is the request's destination: the absolute URL's host, with
// the scheme's port when none is given.
func hostPort(req *http.Request) (string, uint16, bool) {
	if req.URL == nil || !req.URL.IsAbs() || req.URL.Scheme != "http" || req.URL.Host == "" {
		return "", 0, false
	}
	hp := req.URL.Host
	if !strings.Contains(strings.TrimPrefix(hp, "["), "]:") && strings.Count(hp, ":") != 1 {
		hp = net.JoinHostPort(strings.Trim(hp, "[]"), "80")
	}
	host, port, err := config.SplitHostPort(hp)
	if err != nil || host == "" || port == 0 {
		return "", 0, false
	}
	return strings.ToLower(host), port, true
}

func (e *Engine) httpForward(ctx context.Context, workload string, c net.Conn, br *bufio.Reader, req *http.Request) {
	host, port, ok := hostPort(req)
	if !ok {
		httpError(c, http.StatusBadRequest, "send absolute http:// URLs, or CONNECT for https")
		return
	}
	dest := net.JoinHostPort(host, fmt.Sprint(port))
	o := e.classify(ctx, policy.Outbound{Workload: workload, Entry: policy.EntryHTTP, Port: port}, host)
	up, d, ev := e.decideAndDial(ctx, &o, dest)
	if up == nil {
		if d.Verdict == policy.Allow {
			httpError(c, http.StatusBadGateway, "unreachable")
		} else {
			httpError(c, http.StatusForbidden, "refused by torii: "+string(d.Reason))
		}
		return
	}
	defer up.Close()
	e.d.Log.Emit(ev)
	e.inc(workload, 1)
	defer e.inc(workload, -1)
	ub := bufio.NewReader(up)
	for {
		h, p, ok := hostPort(req)
		if !ok || h != host || p != port {
			ev2 := events.Event{Workload: workload, Direction: events.Out, Entry: policy.EntryHTTP, Source: workload,
				Dest: req.URL.Host, Verdict: policy.Refuse, Reason: policy.ReasonHostChanged}
			e.d.Log.Emit(ev2)
			httpError(c, http.StatusForbidden, "one host per connection")
			return
		}
		req.Host = req.URL.Host
		req.RequestURI = ""
		for _, k := range []string{"Proxy-Connection", "Proxy-Authorization"} {
			req.Header.Del(k)
		}
		if err := req.Write(up); err != nil {
			return
		}
		resp, err := http.ReadResponse(ub, req)
		if err != nil {
			httpError(c, http.StatusBadGateway, "bad response")
			return
		}
		err = resp.Write(c)
		resp.Body.Close()
		if err != nil || resp.Close || req.Close {
			return
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Minute))
		next, err := http.ReadRequest(br)
		_ = c.SetReadDeadline(time.Time{})
		if err != nil {
			return
		}
		req = next
	}
}

// readerConn reads first what a bufio.Reader already took from the conn.
type readerConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *readerConn) Read(p []byte) (int, error) { return c.r.Read(p) }
