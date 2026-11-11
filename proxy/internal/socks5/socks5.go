// Package socks5 is the server side of SOCKS5 CONNECT with no
// authentication: dom0 has already vouched for who is calling, over
// qrexec. Names are passed through unresolved, so the proxy decides by
// name (socks5h).
package socks5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
)

// Reply codes.
const (
	Succeeded          byte = 0
	GeneralFailure     byte = 1
	NotAllowed         byte = 2
	NetworkUnreachable byte = 3
	HostUnreachable    byte = 4
	ConnectionRefused  byte = 5
	TTLExpired         byte = 6
	CommandUnsupported byte = 7
	AddressUnsupported byte = 8
)

// Request is a CONNECT request: Host for a name, else Addr.
type Request struct {
	Host string
	Addr netip.Addr
	Port uint16
}

// Target is host:port as the client asked for it.
func (r Request) Target() string {
	if r.Host != "" {
		return fmt.Sprintf("%s:%d", r.Host, r.Port)
	}
	return netip.AddrPortFrom(r.Addr, r.Port).String()
}

// ErrUnsupported is returned after the client has been told so.
var ErrUnsupported = errors.New("socks5: unsupported request")

// Handshake reads the greeting and the request from rw. On an
// unsupported command or address type it replies and returns
// ErrUnsupported.
func Handshake(rw io.ReadWriter) (Request, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(rw, hdr[:]); err != nil {
		return Request{}, err
	}
	if hdr[0] != 5 {
		return Request{}, fmt.Errorf("socks5: version %d", hdr[0])
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(rw, methods); err != nil {
		return Request{}, err
	}
	noAuth := false
	for _, m := range methods {
		if m == 0 {
			noAuth = true
		}
	}
	if !noAuth {
		rw.Write([]byte{5, 0xff})
		return Request{}, ErrUnsupported
	}
	if _, err := rw.Write([]byte{5, 0}); err != nil {
		return Request{}, err
	}
	var req [4]byte
	if _, err := io.ReadFull(rw, req[:]); err != nil {
		return Request{}, err
	}
	if req[0] != 5 {
		return Request{}, fmt.Errorf("socks5: version %d", req[0])
	}
	var r Request
	switch req[3] {
	case 1:
		var b [4]byte
		if _, err := io.ReadFull(rw, b[:]); err != nil {
			return Request{}, err
		}
		r.Addr = netip.AddrFrom4(b)
	case 4:
		var b [16]byte
		if _, err := io.ReadFull(rw, b[:]); err != nil {
			return Request{}, err
		}
		r.Addr = netip.AddrFrom16(b).Unmap()
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(rw, n[:]); err != nil {
			return Request{}, err
		}
		b := make([]byte, n[0])
		if _, err := io.ReadFull(rw, b); err != nil {
			return Request{}, err
		}
		if a, err := netip.ParseAddr(string(b)); err == nil {
			r.Addr = a.Unmap()
		} else {
			r.Host = string(b)
		}
	default:
		Reply(rw, AddressUnsupported)
		return Request{}, ErrUnsupported
	}
	var p [2]byte
	if _, err := io.ReadFull(rw, p[:]); err != nil {
		return Request{}, err
	}
	r.Port = binary.BigEndian.Uint16(p[:])
	if req[1] != 1 { // only CONNECT
		Reply(rw, CommandUnsupported)
		return Request{}, ErrUnsupported
	}
	return r, nil
}

// Reply sends a reply with a zero bound address.
func Reply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}
