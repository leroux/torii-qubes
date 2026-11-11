package sni

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// clientHello returns the first record Go's own TLS client sends.
func clientHello(t testing.TB, serverName string) []byte {
	t.Helper()
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		c := tls.Client(a, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
		_ = c.Handshake()
	}()
	b.SetReadDeadline(time.Now().Add(2 * time.Second))
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(b, hdr); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint16(hdr[3:]))
	if _, err := io.ReadFull(b, body); err != nil {
		t.Fatal(err)
	}
	return append(hdr, body...)
}

// fragment splits one handshake record into records of at most n bytes
// of payload each, as RFC 8446 5.1 allows.
func fragment(rec []byte, n int) []byte {
	var out []byte
	for body := rec[5:]; len(body) > 0; {
		k := min(n, len(body))
		out = append(out, rec[0], rec[1], rec[2], byte(k>>8), byte(k))
		out = append(out, body[:k]...)
		body = body[k:]
	}
	return out
}

// peek sends wire (in writes of at most chunk bytes, pausing between
// them; chunk 0 is one write), then extra, and peeks the other end. It
// checks that prefix plus what remains is exactly what was sent.
func peek(t *testing.T, wire, extra []byte, chunk int, wait time.Duration) (string, error) {
	t.Helper()
	a, b := net.Pipe()
	defer b.Close()
	sent := append(append([]byte{}, wire...), extra...)
	go func() {
		defer a.Close()
		for rest := wire; len(rest) > 0; {
			k := len(rest)
			if chunk > 0 {
				k = min(chunk, k)
				time.Sleep(time.Millisecond)
			}
			if _, err := a.Write(rest[:k]); err != nil {
				return
			}
			rest = rest[k:]
		}
		a.Write(extra)
	}()
	name, prefix, err := Peek(b, wait)
	if err == nil {
		rest, _ := io.ReadAll(b)
		if got := append(prefix, rest...); !bytes.Equal(got, sent) {
			t.Errorf("stream changed: sent %d bytes, prefix+rest is %d", len(sent), len(got))
		}
	}
	return name, err
}

func TestPeekGoClient(t *testing.T) {
	for _, name := range []string{"pypi.org", "Files.PythonHosted.org"} {
		got, err := peek(t, clientHello(t, name), []byte("after"), 0, 2*time.Second)
		if err != nil || got != strings.ToLower(name) {
			t.Errorf("%s: got %q %v", name, got, err)
		}
	}
}

// A ClientHello split across records or TCP segments still has its name
// read: hiding it there must not pass as "no name sent".
func TestPeekSplitClientHello(t *testing.T) {
	rec := clientHello(t, "evil.com")
	for _, c := range []struct {
		what  string
		wire  []byte
		chunk int
	}{
		{"records of 100 bytes", fragment(rec, 100), 0},
		{"records of 1 byte", fragment(rec, 1), 0},
		{"segments of 7 bytes", rec, 7},
		{"records and segments", fragment(rec, 50), 3},
	} {
		got, err := peek(t, c.wire, nil, c.chunk, 2*time.Second)
		if err != nil || got != "evil.com" {
			t.Errorf("%s: got %q %v", c.what, got, err)
		}
	}
}

func TestPeekNoSNIAndNotTLS(t *testing.T) {
	// InsecureSkipVerify with no name: no SNI sent.
	if got, err := peek(t, clientHello(t, ""), nil, 0, 2*time.Second); err != nil || got != "" {
		t.Errorf("no sni: %q %v", got, err)
	}
	if got, err := peek(t, []byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"), nil, 0, time.Second); err != nil || got != "" {
		t.Errorf("http: %q %v", got, err)
	}
	// 0x16 but no TLS record version: not TLS.
	if got, err := peek(t, []byte{0x16, 0x01, 0, 0}, nil, 0, time.Second); err != nil || got != "" {
		t.Errorf("0x16 0x01: %q %v", got, err)
	}

	// Nothing in time is not an answer: a ClientHello after a pause longer
	// than wait is still read. (The pause is the case under test.)
	a, b := net.Pipe()
	defer a.Close()
	rec := clientHello(t, "evil.com")
	go func() { time.Sleep(300 * time.Millisecond); a.Write(rec) }()
	if got, _, err := Peek(b, 100*time.Millisecond); got != "evil.com" || err != nil {
		t.Errorf("after a pause: %q %v", got, err)
	}
	// Closed before sending anything: the read error, never "".
	a2, b2 := net.Pipe()
	a2.Close()
	if _, _, err := Peek(b2, 100*time.Millisecond); err == nil {
		t.Error("closed: no error")
	}
}

// What starts as TLS but has no readable ClientHello is ErrUnreadable,
// never "".
func TestPeekUnreadable(t *testing.T) {
	rec := clientHello(t, "evil.com")
	junk := []byte{0x16, 3, 1, 0, 8, 1, 0, 0, 4, 0xde, 0xad, 0xbe, 0xef}
	for _, c := range []struct {
		what string
		wire []byte
	}{
		{"cut short", rec[:len(rec)/2]},
		{"cut in the header", rec[:3]},
		{"only 0x16", rec[:1]},
		{"junk hello", junk},
		{"not a ClientHello", []byte{0x16, 3, 3, 0, 4, 2, 0, 0, 0}},
	} {
		if got, err := peek(t, c.wire, nil, 0, time.Second); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: got %q %v", c.what, got, err)
		}
	}

	// Half a ClientHello, then silence: unreadable once wait is up.
	a, b := net.Pipe()
	defer a.Close()
	go a.Write(rec[:len(rec)/2])
	start := time.Now()
	if _, _, err := Peek(b, 200*time.Millisecond); !errors.Is(err, ErrUnreadable) {
		t.Errorf("slow: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("waited too long")
	}
}

func FuzzPeek(f *testing.F) {
	rec := clientHello(f, "pypi.org")
	f.Add(rec)
	f.Add(fragment(rec, 64))
	f.Add([]byte{0x16, 3, 1, 0, 4, 1, 0, 0, 0})
	f.Fuzz(func(t *testing.T, wire []byte) {
		a, b := net.Pipe()
		defer b.Close()
		go func() { a.Write(wire); a.Close() }()
		name, _, err := Peek(b, time.Second)
		if err == nil && strings.ContainsAny(name, " /\\\x00") {
			t.Errorf("name %q", name)
		}
	})
}
