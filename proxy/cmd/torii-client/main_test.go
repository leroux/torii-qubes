package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leroux/torii-qubes/claims"
)

func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

func TestTheClientListensOnLoopbackOnly(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_N3N8, "loopback-only")
	if loopback != "127.0.0.1" {
		t.Fatalf("the default address is %s", loopback)
	}
	unit, err := os.ReadFile("../../../packaging/client/torii-client.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart=/usr/lib/torii/torii-client\n") {
		t.Error("the unit passes arguments that may move the client off loopback")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &clientConfig{Listen: []listen{{Port: freePort(t), Service: "torii.Connect+socks", Target: "@default"}}}
	lns, err := listenAll(ctx, loopback, cfg, "true")
	if err != nil {
		t.Fatal(err)
	}
	for _, ln := range lns {
		defer ln.Close()
		if a := ln.Addr().(*net.TCPAddr); !a.IP.IsLoopback() {
			t.Errorf("listening on %s", a)
		}
	}
}

func TestEachConnectionIsOneQrexecCall(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_N3N8, "one-call-per-connection")
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "qrexec-client-vm")
	// The fake records its arguments and answers on stdout.
	script := fmt.Sprintf("#!/bin/sh\necho \"$1 $2\" >> %s\necho answered\n", calls)
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	port := freePort(t)
	cfg := &clientConfig{Listen: []listen{{Port: port, Service: "torii.Connect+f.db", Target: "@default"}}}
	lns, err := listenAll(ctx, loopback, cfg, fake)
	if err != nil {
		t.Fatal(err)
	}
	defer lns[0].Close()
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(5 * time.Second))
		b := make([]byte, 64)
		n, _ := c.Read(b)
		if string(b[:n]) != "answered\n" {
			t.Errorf("connection %d got %q", i, b[:n])
		}
		c.Close()
	}
	b, _ := os.ReadFile(calls)
	if string(b) != "@default torii.Connect+f.db\n@default torii.Connect+f.db\n" {
		t.Errorf("calls:\n%s", b)
	}
}

func TestABadEntryRefusesTheWholeList(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_N3N8, "bad-service-not-listened")
	good := `{"port": 1080, "service": "torii.Connect+socks", "target": "@default"}`
	for _, bad := range []string{
		`{"port": 22, "service": "qubes.VMShell", "target": "@default"}`,
		`{"port": 22, "service": "torii.Connect+socks", "target": "$(evil)"}`,
		`{"port": 1080, "service": "torii.Connect+http", "target": "@default"}`,
		`{"port": 0, "service": "torii.Connect+http", "target": "@default"}`,
	} {
		if c, err := parse([]byte(`{"listen": [` + good + `, ` + bad + `]}`)); err == nil {
			t.Errorf("accepted %s: %+v", bad, c)
		}
	}
	if c, err := parse([]byte(`{"listen": [` + good + `]}`)); err != nil || len(c.Listen) != 1 {
		t.Errorf("refused a good list: %v", err)
	}
}
