package engine

import (
	"bufio"
	"context"
	"crypto/tls"
	"net/netip"
	"testing"
	"time"
)

// A second identity on one qube terminates TLS with its own certificate
// and reaches that qube's plain port over qrexec.
func TestInboundTLSOnASecondIdentity(t *testing.T) {
	w := newWorld(t)
	w.qubes.serve(t, "airgap", 3000, "plain-backend")
	w.start(t)
	web := w.net.Node("airgap-web")
	c, err := w.net.Node("laptop").Dial(context.Background(), netip.AddrPortFrom(web.IP(), 443))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tc := tls.Client(c, &tls.Config{ServerName: "airgap-web.fake.ts.net", InsecureSkipVerify: true})
	tc.SetDeadline(time.Now().Add(3 * time.Second))
	if err := tc.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if cn := tc.ConnectionState().PeerCertificates[0].Subject.CommonName; cn != "airgap-web.fake.ts.net" {
		t.Errorf("certificate for %q", cn)
	}
	line, err := bufio.NewReader(tc).ReadString('\n')
	if err != nil || line != "plain-backend\n" {
		t.Fatalf("%q %v", line, err)
	}
	// The airgap qube's own identity still has only its own port.
	if c2, err := w.net.Node("laptop").Dial(context.Background(), netip.AddrPortFrom(web.IP(), 8443)); err == nil {
		c2.Close()
		t.Error("airgap-web answers on airgap's port")
	}
}
