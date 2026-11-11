// Package sni reads the server name a TLS client sends, without
// decrypting anything and without consuming the bytes: the relay passes
// the ClientHello on unchanged.
//
// The ClientHello is parsed by crypto/tls, so a hello split across
// several TLS records, or across TCP segments, is read like any other.
package sni

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// ErrUnreadable: the connection opens with a TLS handshake record, but no
// server name could be read from it (malformed, cut short, or too slow).
// Callers deciding by server name must refuse it: treating it as "no name
// sent" would let a client hide its name by sending an odd ClientHello.
var ErrUnreadable = errors.New("sni: TLS ClientHello could not be read")

// errGotHello stops the handshake once the ClientHello is in.
var errGotHello = errors.New("sni: got ClientHello")

// Peek waits for the client's first bytes, however long that takes, and
// returns the server name of a ClientHello, lowercased. wait bounds the
// rest: once the client has started, the whole ClientHello must arrive
// within it. Waiting has no outcome of its own, so a client cannot turn
// the check off by pausing before its ClientHello; the caller lets a
// server that speaks first do so meanwhile. It returns "" with a nil
// error when the client sends something other than TLS or a ClientHello
// with no server name; ErrUnreadable when it sends a TLS record but no
// ClientHello can be read in time; the read error when the client closes
// or fails before sending anything. prefix is every byte read from c, to
// be sent upstream before the rest of c.
func Peek(c net.Conn, wait time.Duration) (name string, prefix []byte, err error) {
	_ = c.SetReadDeadline(time.Time{})
	rec := &recorder{r: c}
	var hdr [2]byte
	if _, err := io.ReadFull(rec, hdr[:1]); err != nil {
		return "", nil, err
	}
	if hdr[0] != 0x16 { // not a handshake record
		return "", rec.buf, nil
	}
	_ = c.SetReadDeadline(time.Now().Add(wait))
	defer c.SetReadDeadline(time.Time{})
	if _, err := io.ReadFull(rec, hdr[1:]); err != nil {
		return "", rec.buf, ErrUnreadable
	}
	if hdr[1] != 3 {
		return "", rec.buf, nil // 0x16 but not TLS (record version 3.x)
	}

	// Hand crypto/tls the bytes it has seen, then the rest of c, and stop
	// it once it has parsed the ClientHello. It writes nothing to c.
	var hello *tls.ClientHelloInfo
	srv := tls.Server(&helloConn{Conn: c, r: io.MultiReader(&replay{b: rec.buf}, rec)}, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = h
			return nil, errGotHello
		},
	})
	_ = srv.Handshake() // bounded by c's read deadline
	if hello == nil {
		return "", rec.buf, ErrUnreadable
	}
	name = strings.ToLower(hello.ServerName)
	if strings.ContainsAny(name, " /\\\x00") {
		return "", rec.buf, ErrUnreadable
	}
	return name, rec.buf, nil
}

// recorder keeps a copy of every byte read through it.
type recorder struct {
	r   io.Reader
	buf []byte
}

func (r *recorder) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.buf = append(r.buf, p[:n]...)
	return n, err
}

// replay yields b once, without touching the recorder's buffer.
type replay struct{ b []byte }

func (r *replay) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}

// helloConn is what crypto/tls handshakes over: reads come from r,
// writes (an alert, at most) go nowhere, so the client sees nothing.
type helloConn struct {
	net.Conn
	r io.Reader
}

func (c *helloConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *helloConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *helloConn) Close() error                { return nil }
