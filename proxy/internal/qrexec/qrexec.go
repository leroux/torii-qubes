// Package qrexec is the program's side of qrexec.
//
// Outbound, the workload calls the proxy: torii.Connect+<arg> arrives at
// a unix socket the daemon listens on, either directly as a Qubes
// socket-based service (qrexec-agent writes the header) or through the
// exec helper (ExecService), which writes the same header from the
// environment qrexec gives every service. Either way the caller's name
// comes from dom0, never from the workload.
//
// Inbound, the proxy calls the workload: DialQube runs
// qrexec-client-vm <qube> qubes.ConnectTCP+<port>, the stock service that
// connects to a port inside the workload, allowed per port by dom0 policy.
package qrexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Service is the service name workloads call.
const Service = "torii.Connect"

// Header is what precedes the data on the connect socket:
// "<service>+<arg> <caller>\0". This is the header qrexec-agent sends to
// socket-based services (to confirm on the test machine); ExecService
// sends the same.
type Header struct {
	Service string
	Arg     string
	Caller  string
}

var (
	argRe  = regexp.MustCompile(`^[A-Za-z0-9_.+-]{0,64}$`)
	qubeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,30}$`)
)

// ErrHeader: the header is malformed.
var ErrHeader = errors.New("qrexec: bad header")

// ReadHeader reads the header from r. The reader may have buffered data
// beyond it; callers use the returned reader for the rest.
//
//charter:realizes SW-261127-7C3Y
func ReadHeader(r *bufio.Reader) (Header, error) {
	line, err := r.ReadString(0)
	if err != nil {
		return Header{}, ErrHeader
	}
	line = strings.TrimSuffix(line, "\x00")
	if len(line) > 200 {
		return Header{}, ErrHeader
	}
	svc, caller, ok := strings.Cut(line, " ")
	if !ok {
		return Header{}, ErrHeader
	}
	name, arg, _ := strings.Cut(svc, "+")
	if name != Service || !argRe.MatchString(arg) || !qubeRe.MatchString(caller) {
		return Header{}, ErrHeader
	}
	return Header{Service: name, Arg: arg, Caller: caller}, nil
}

// Format renders a header.
func (h Header) Format() string { return fmt.Sprintf("%s+%s %s\x00", h.Service, h.Arg, h.Caller) }

// Handler serves one call.
type Handler func(ctx context.Context, caller, arg string, c net.Conn)

// Serve accepts calls on ln (the connect socket) until it closes.
func Serve(ctx context.Context, ln net.Listener, h Handler) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func() {
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			br := bufio.NewReader(c)
			hdr, err := ReadHeader(br)
			c.SetReadDeadline(time.Time{})
			if err != nil {
				c.Close()
				return
			}
			h(ctx, hdr.Caller, hdr.Arg, &bufConn{Conn: c, r: br})
		}()
	}
}

// ListenUnix listens on a fresh unix socket with mode perm.
func ListenUnix(path string, perm os.FileMode) (net.Listener, error) {
	_ = os.Remove(path)
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, perm); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

// ExecService is the body of /etc/qubes-rpc/torii.Connect when it is a
// program: it forwards stdin and stdout to the daemon's socket, with the
// caller and argument qrexec put in the environment.
//
//charter:realizes SW-261127-7C3Y
func ExecService(socket string, env func(string) string, stdin io.Reader, stdout io.Writer) error {
	caller := env("QREXEC_REMOTE_DOMAIN")
	arg := env("QREXEC_SERVICE_ARGUMENT")
	h := Header{Service: Service, Arg: arg, Caller: caller}
	if !qubeRe.MatchString(caller) || !argRe.MatchString(arg) {
		return ErrHeader
	}
	c, err := net.Dial("unix", socket)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := io.WriteString(c, h.Format()); err != nil {
		return err
	}
	return splice(c, stdin, stdout)
}

func splice(c net.Conn, in io.Reader, out io.Writer) error {
	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(c, in)
		if uc, ok := c.(*net.UnixConn); ok {
			uc.CloseWrite()
		}
		errc <- err
	}()
	_, err := io.Copy(out, c)
	return err
}

// Dialer is the real QubeDialer: one qrexec call per connection.
type Dialer struct {
	// Command is qrexec-client-vm by default; tests point it elsewhere.
	Command string
}

// DialQube calls qubes.ConnectTCP+<port> in qube.
func (d *Dialer) DialQube(ctx context.Context, qube string, port uint16) (net.Conn, error) {
	if !qubeRe.MatchString(qube) {
		return nil, fmt.Errorf("qrexec: bad qube name %q", qube)
	}
	cmdName := d.Command
	if cmdName == "" {
		cmdName = "qrexec-client-vm"
	}
	// The call outlives ctx (a dial timeout); it ends when the conn closes.
	cmd := exec.Command(cmdName, qube, fmt.Sprintf("qubes.ConnectTCP+%d", port))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pc := &pipeConn{cmd: cmd, w: stdin, r: stdout, qube: qube, port: port}
	return pc, nil
}

// pipeConn is a net.Conn over a child's stdin and stdout.
type pipeConn struct {
	cmd  *exec.Cmd
	w    io.WriteCloser
	r    io.ReadCloser
	qube string
	port uint16
	once sync.Once
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeConn) CloseWrite() error           { return p.w.Close() }
func (p *pipeConn) Close() error {
	p.once.Do(func() {
		p.w.Close()
		p.r.Close()
		if p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
		p.cmd.Wait()
	})
	return nil
}
func (p *pipeConn) LocalAddr() net.Addr                { return qrexecAddr("proxy") }
func (p *pipeConn) RemoteAddr() net.Addr               { return qrexecAddr(fmt.Sprintf("%s:%d", p.qube, p.port)) }
func (p *pipeConn) SetDeadline(t time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return nil }

type qrexecAddr string

func (a qrexecAddr) Network() string { return "qrexec" }
func (a qrexecAddr) String() string  { return string(a) }

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *bufConn) CloseWrite() error {
	if uc, ok := b.Conn.(*net.UnixConn); ok {
		return uc.CloseWrite()
	}
	return nil
}
