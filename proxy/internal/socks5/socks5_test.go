package socks5

import (
	"context"
	"io"
	"net"
	"testing"

	"golang.org/x/net/proxy"
)

func TestHandshakeWithXNetClient(t *testing.T) {
	for _, target := range []string{"pypi.org:443", "10.1.2.3:80", "[fd00::1]:22"} {
		a, b := net.Pipe()
		got := make(chan Request, 1)
		go func() {
			r, err := Handshake(b)
			if err != nil {
				t.Error(err)
			}
			Reply(b, Succeeded)
			got <- r
			io.Copy(b, b)
		}()
		d, _ := proxy.SOCKS5("tcp", "unused", nil, dialerFunc(func(context.Context, string, string) (net.Conn, error) { return a, nil }))
		c, err := d.(proxy.ContextDialer).DialContext(context.Background(), "tcp", target)
		if err != nil {
			t.Fatal(err)
		}
		r := <-got
		if r.Target() != target {
			t.Errorf("target %q want %q", r.Target(), target)
		}
		c.Close()
	}
}

func TestRefusedReachesClient(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		Handshake(b)
		Reply(b, NotAllowed)
		b.Close()
	}()
	d, _ := proxy.SOCKS5("tcp", "unused", nil, dialerFunc(func(context.Context, string, string) (net.Conn, error) { return a, nil }))
	if _, err := d.(proxy.ContextDialer).DialContext(context.Background(), "tcp", "evil.com:443"); err == nil {
		t.Fatal("client saw success")
	}
}

func TestUnsupportedCommand(t *testing.T) {
	a, b := net.Pipe()
	go a.Write([]byte{5, 1, 0, 5, 3, 0, 1, 1, 2, 3, 4, 0, 53}) // UDP ASSOCIATE
	go io.Copy(io.Discard, a)
	if _, err := Handshake(b); err != ErrUnsupported {
		t.Fatalf("err %v", err)
	}
}

type dialerFunc func(ctx context.Context, n, a string) (net.Conn, error)

func (f dialerFunc) Dial(n, a string) (net.Conn, error) { return f(context.Background(), n, a) }
func (f dialerFunc) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return f(ctx, n, a)
}
