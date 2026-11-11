package qrexec

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// When run as the fake qrexec-client-vm, the test binary connects its
// stdin and stdout to FAKE_QREXEC_TARGET, as qubes.ConnectTCP would.
func TestMain(m *testing.M) {
	if target := os.Getenv("FAKE_QREXEC_TARGET"); target != "" {
		if len(os.Args) != 3 || !strings.HasPrefix(os.Args[2], "qubes.ConnectTCP+") {
			fmt.Fprintln(os.Stderr, "bad args", os.Args)
			os.Exit(2)
		}
		c, err := net.Dial("tcp", target)
		if err != nil {
			os.Exit(1)
		}
		go io.Copy(c, os.Stdin)
		io.Copy(os.Stdout, c)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHeader(t *testing.T) {
	h, err := ReadHeader(bufio.NewReader(strings.NewReader("lintel.Connect+socks job-1\x00rest")))
	if err != nil || h.Arg != "socks" || h.Caller != "job-1" {
		t.Fatalf("%+v %v", h, err)
	}
	for _, bad := range []string{
		"lintel.Connect+socks job-1",       // no terminator
		"qubes.ConnectTCP+22 job-1\x00",   // other service
		"lintel.Connect+socks $(evil)\x00", // bad caller
		"lintel.Connect+a/b job-1\x00",     // bad arg
		"lintel.Connect+socks\x00",         // no caller
	} {
		if _, err := ReadHeader(bufio.NewReader(strings.NewReader(bad))); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// The exec helper is an ordinary program on stdin and stdout with the
// caller in the environment; the daemon sees the caller dom0 named.
func TestExecServiceReachesDaemon(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "connect.sock")
	ln, err := ListenUnix(sock, 0o660)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go Serve(context.Background(), ln, func(ctx context.Context, caller, arg string, c net.Conn) {
		defer c.Close()
		got <- caller + " " + arg
		b, _ := io.ReadAll(c)
		fmt.Fprintf(c, "echo:%s", b)
	})
	env := map[string]string{"QREXEC_REMOTE_DOMAIN": "job-1", "QREXEC_SERVICE_ARGUMENT": "f.db"}
	var out bytes.Buffer
	if err := ExecService(sock, func(k string) string { return env[k] }, strings.NewReader("hello"), &out); err != nil {
		t.Fatal(err)
	}
	if g := <-got; g != "job-1 f.db" {
		t.Errorf("daemon saw %q", g)
	}
	if out.String() != "echo:hello" {
		t.Errorf("out %q", out.String())
	}
	env["QREXEC_REMOTE_DOMAIN"] = "bad name"
	if err := ExecService(sock, func(k string) string { return env[k] }, strings.NewReader(""), io.Discard); err == nil {
		t.Error("bad caller passed on")
	}
	if fi, _ := os.Stat(sock); fi.Mode().Perm() != 0o660 {
		t.Errorf("socket mode %v", fi.Mode())
	}
}

func TestDialQubeRunsQrexecClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		fmt.Fprintln(c, "inside the workload")
		io.Copy(c, c)
		c.Close()
	}()
	t.Setenv("FAKE_QREXEC_TARGET", ln.Addr().String())
	self, _ := os.Executable()
	d := &Dialer{Command: self}
	c, err := d.DialQube(context.Background(), "infra", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "inside the workload\n" {
		t.Fatalf("%q %v", line, err)
	}
	if _, err := d.DialQube(context.Background(), "../etc", 1); err == nil {
		t.Error("bad qube name passed to qrexec")
	}
	_ = time.Second
}
