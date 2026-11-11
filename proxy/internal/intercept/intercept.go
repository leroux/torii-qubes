//go:build linux

// Package intercept carries routed workloads' traffic in the proxy qube:
// nftables redirects every TCP connection and DNS query arriving from a
// downstream interface to the program, kernel forwarding stays off, and
// the original destination is read back with SO_ORIGINAL_DST.
package intercept

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Rules returns the nftables ruleset for the proxy qube. downstream is
// the interface pattern workloads arrive on ("vif*" in Qubes).
//
//   - prerouting redirects DNS (UDP and TCP 53) to dnsPort and every
//     other TCP connection to tcpPort;
//   - forward drops everything (forwarding is also off by sysctl), so
//     nothing passes that the program did not relay;
//   - mangle hands every other IPv4 UDP datagram to udpPort by TPROXY,
//     keeping its destination; a policy route delivers marked packets
//     locally;
//   - input from downstream accepts the intercept and DNS ports, what
//     was tproxied, and ICMP. A connection made straight to the
//     intercept port is accepted too, not dropped: the engine refuses
//     it as the proxy qube's own address, and logs it.
func Rules(downstream string, tcpPort, dnsPort, udpPort uint16) string {
	return fmt.Sprintf(`table inet lintel
delete table inet lintel
table inet lintel {
	chain prerouting {
		type nat hook prerouting priority dstnat - 10; policy accept;
		iifname %[1]q udp dport 53 redirect to :%[3]d
		iifname %[1]q tcp dport 53 redirect to :%[3]d
		iifname %[1]q meta l4proto tcp redirect to :%[2]d
	}
	chain mangle {
		type filter hook prerouting priority mangle; policy accept;
		iifname %[1]q meta nfproto ipv4 udp dport != 53 meta mark set %#[5]x tproxy ip to :%[4]d
	}
	chain input {
		type filter hook input priority filter - 10; policy accept;
		iifname %[1]q ct state established,related accept
		iifname %[1]q tcp dport %[2]d accept
		iifname %[1]q meta l4proto { tcp, udp } th dport %[3]d accept
		iifname %[1]q meta mark %#[5]x meta l4proto udp accept
		iifname %[1]q meta l4proto { icmp, ipv6-icmp } accept
		iifname %[1]q counter drop
	}
	chain forward {
		type filter hook forward priority filter - 10; policy drop;
	}
}
`, downstream, tcpPort, dnsPort, udpPort, TProxyMark)
}

// Install applies the rules with nft and turns forwarding off.
func Install(downstream string, tcpPort, dnsPort, udpPort uint16) error {
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(Rules(downstream, tcpPort, dnsPort, udpPort))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %v: %s", err, out)
	}
	if err := qubesInputAccept(downstream, tcpPort, dnsPort); err != nil {
		return err
	}
	// Marked (tproxied) packets are delivered locally. Replace, so a
	// restart does not stack rules.
	mark, table := fmt.Sprintf("%#x", TProxyMark), fmt.Sprint(TProxyTable)
	exec.Command("ip", "rule", "del", "fwmark", mark, "lookup", table).Run()
	for _, args := range [][]string{
		{"rule", "add", "fwmark", mark, "lookup", table},
		{"route", "replace", "local", "0.0.0.0/0", "dev", "lo", "table", table},
	} {
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("ip %v: %v: %s", args, err, out)
		}
	}
	for _, p := range []string{"/proc/sys/net/ipv4/ip_forward", "/proc/sys/net/ipv6/conf/all/forwarding"} {
		if err := os.WriteFile(p, []byte("0\n"), 0o644); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// OriginalDst returns where a redirected connection was going.
func OriginalDst(c *net.TCPConn) (netip.AddrPort, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, err
	}
	var out netip.AddrPort
	var serr error
	local, _ := netip.ParseAddrPort(c.LocalAddr().String())
	err = rc.Control(func(fd uintptr) {
		if local.Addr().Unmap().Is4() {
			var sa unix.RawSockaddrInet4
			l := uint32(unsafe.Sizeof(sa))
			_, _, e := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, unix.SOL_IP, unix.SO_ORIGINAL_DST,
				uintptr(unsafe.Pointer(&sa)), uintptr(unsafe.Pointer(&l)), 0)
			if e != 0 {
				serr = e
				return
			}
			port := binary.BigEndian.Uint16((*[2]byte)(unsafe.Pointer(&sa.Port))[:])
			out = netip.AddrPortFrom(netip.AddrFrom4(sa.Addr), port)
			return
		}
		var sa unix.RawSockaddrInet6
		l := uint32(unsafe.Sizeof(sa))
		_, _, e := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, unix.SOL_IPV6, ip6tSOOriginalDst,
			uintptr(unsafe.Pointer(&sa)), uintptr(unsafe.Pointer(&l)), 0)
		if e != 0 {
			serr = e
			return
		}
		port := binary.BigEndian.Uint16((*[2]byte)(unsafe.Pointer(&sa.Port))[:])
		out = netip.AddrPortFrom(netip.AddrFrom16(sa.Addr).Unmap(), port)
	})
	if err != nil {
		return netip.AddrPort{}, err
	}
	return out, serr
}

// Handler is what the engine provides.
type Handler interface {
	HandleIntercepted(ctx context.Context, c net.Conn, src, dst netip.AddrPort)
	AnswerDNS(ctx context.Context, src netip.Addr, query []byte) []byte
}

// ServeTCP accepts redirected connections until ln closes.
func ServeTCP(ctx context.Context, ln net.Listener, h Handler) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		tc := c.(*net.TCPConn)
		dst, err := OriginalDst(tc)
		src, _ := netip.ParseAddrPort(c.RemoteAddr().String())
		if err != nil {
			c.Close()
			continue
		}
		// A connection to the intercept port itself (dst is this listener)
		// is handed on like any other: the engine refuses the proxy qube's
		// own addresses, and logs it, so every refusal leaves evidence.
		go h.HandleIntercepted(ctx, c, netip.AddrPortFrom(src.Addr().Unmap(), src.Port()), dst)
	}
}

// ServeDNSUDP answers redirected DNS over UDP until pc closes. With
// REDIRECT the reply leaves from the redirected-to address, and conntrack
// rewrites it back to the address the workload asked.
func ServeDNSUDP(ctx context.Context, pc net.PacketConn, h Handler) error {
	buf := make([]byte, 65535)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		q := append([]byte(nil), buf[:n]...)
		src := from.(*net.UDPAddr).AddrPort()
		go func() {
			if out := h.AnswerDNS(ctx, src.Addr().Unmap(), q); out != nil {
				pc.WriteTo(out, from)
			}
		}()
	}
}

// ServeDNSTCP answers DNS over TCP until ln closes.
func ServeDNSTCP(ctx context.Context, ln net.Listener, h Handler) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer c.Close()
			src, _ := netip.ParseAddrPort(c.RemoteAddr().String())
			for {
				var l [2]byte
				if _, err := readFull(c, l[:]); err != nil {
					return
				}
				q := make([]byte, binary.BigEndian.Uint16(l[:]))
				if _, err := readFull(c, q); err != nil {
					return
				}
				out := h.AnswerDNS(ctx, src.Addr().Unmap(), q)
				if out == nil {
					return
				}
				binary.BigEndian.PutUint16(l[:], uint16(len(out)))
				if _, err := c.Write(append(l[:], out...)); err != nil {
					return
				}
			}
		}()
	}
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := c.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// TProxyMark marks tproxied UDP; TProxyTable is its local route table.
const (
	TProxyMark  = 0x7173
	TProxyTable = 7173
)

// UDPHandler is what the engine provides for UDP.
type UDPHandler interface {
	HandleUDP(ctx context.Context, src, dst netip.AddrPort, b []byte, open func() (engineUDPReply, error))
}

// ListenUDP opens the transparent UDP socket TPROXY delivers to, asking
// the kernel for each datagram's original destination.
func ListenUDP(addr string) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		err := rc.Control(func(fd uintptr) {
			if serr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_TRANSPARENT, 1); serr != nil {
				return
			}
			serr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_RECVORIGDSTADDR, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", addr)
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

// origDst reads the original destination from a datagram's control data.
func origDst(oob []byte) (netip.AddrPort, bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return netip.AddrPort{}, false
	}
	for _, m := range msgs {
		if m.Header.Level == unix.SOL_IP && m.Header.Type == unix.IP_ORIGDSTADDR && len(m.Data) >= unix.SizeofSockaddrInet4 {
			sa := (*unix.RawSockaddrInet4)(unsafe.Pointer(&m.Data[0]))
			port := binary.BigEndian.Uint16((*[2]byte)(unsafe.Pointer(&sa.Port))[:])
			return netip.AddrPortFrom(netip.AddrFrom4(sa.Addr), port), true
		}
	}
	return netip.AddrPort{}, false
}

// ServeUDP reads tproxied datagrams until pc closes.
func ServeUDP(ctx context.Context, pc *net.UDPConn, h UDPHandler) error {
	buf := make([]byte, 65535)
	oob := make([]byte, 128)
	for {
		n, oobn, _, src, err := pc.ReadMsgUDPAddrPort(buf, oob)
		if err != nil {
			return err
		}
		dst, ok := origDst(oob[:oobn])
		if !ok {
			continue
		}
		src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
		b := append([]byte(nil), buf[:n]...)
		h.HandleUDP(ctx, src, dst, b, func() (engineUDPReply, error) { return newReply(src, dst) })
	}
}

// reply sends from the workload's destination address, through a
// transparent socket bound there, so the workload sees the real peer.
type reply struct {
	c   *net.UDPConn
	dst *net.UDPAddr
}

func newReply(to, from netip.AddrPort) (*reply, error) {
	lc := net.ListenConfig{Control: func(_, _ string, rc syscall.RawConn) error {
		var serr error
		err := rc.Control(func(fd uintptr) {
			if serr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_TRANSPARENT, 1); serr != nil {
				return
			}
			serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", from.String())
	if err != nil {
		return nil, err
	}
	return &reply{c: pc.(*net.UDPConn), dst: net.UDPAddrFromAddrPort(to)}, nil
}

func (r *reply) Send(b []byte) error { _, err := r.c.WriteToUDP(b, r.dst); return err }
func (r *reply) Close() error        { return r.c.Close() }

// ip6tSOOriginalDst is IP6T_SO_ORIGINAL_DST from linux/netfilter_ipv6/ip6_tables.h.
const ip6tSOOriginalDst = 80

// QubesComment marks the rules this program adds to Qubes' own tables.
const QubesComment = "lintel"

// QubesInputRules are the accepts a Qubes netvm needs: its own input chain
// (table ip qubes, chain input) rejects whatever arrives from downstream
// (iifgroup 2) for the netvm itself, and an accept in another table does
// not override that. Qubes leaves chain custom-input, jumped to first,
// for exactly this.
func QubesInputRules(downstream string, tcpPort, dnsPort uint16) []string {
	c := fmt.Sprintf("comment %q", QubesComment)
	return []string{
		fmt.Sprintf("iifname %q tcp dport %d accept %s", downstream, tcpPort, c),
		fmt.Sprintf("iifname %q meta l4proto { tcp, udp } th dport %d accept %s", downstream, dnsPort, c),
		fmt.Sprintf("iifname %q meta mark %#x meta l4proto udp accept %s", downstream, TProxyMark, c),
	}
}

// qubesInputAccept replaces this program's rules in ip qubes custom-input,
// leaving everyone else's. Outside Qubes (no such chain) it does nothing.
func qubesInputAccept(downstream string, tcpPort, dnsPort uint16) error {
	out, err := exec.Command("nft", "-a", "list", "chain", "ip", "qubes", "custom-input").Output()
	if err != nil {
		return nil // not a Qubes netvm
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, fmt.Sprintf("comment %q", QubesComment)) {
			continue
		}
		if i := strings.LastIndex(line, "# handle "); i >= 0 {
			h := strings.TrimSpace(line[i+len("# handle "):])
			exec.Command("nft", "delete", "rule", "ip", "qubes", "custom-input", "handle", h).Run()
		}
	}
	for _, r := range QubesInputRules(downstream, tcpPort, dnsPort) {
		args := append([]string{"add", "rule", "ip", "qubes", "custom-input"}, strings.Fields(r)...)
		// The comment's quotes must reach nft as one word.
		args = append(args[:len(args)-2], "comment", fmt.Sprintf("%q", QubesComment))
		if o, err := exec.Command("nft", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("nft add rule ip qubes custom-input: %v: %s", err, o)
		}
	}
	return nil
}
