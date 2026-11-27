package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leroux/torii-qubes/claims"
)

// A change whose config fails its check is not taken: the proxy goes on
// with the config it runs, and takes the next valid one.
func TestABadConfigNeverReplacesTheRunningOne(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_XGDY, "bad-config-kept-out")
	path := filepath.Join(t.TempDir(), "config.json")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	valid := func(v string) string {
		return `{"version": "` + v + `", "proxy_qube": "p", "state_dir": "/s", "workloads": {}}`
	}
	write(valid("v1"))
	if c, err := loadConfig(path); err != nil || c.Version != "v1" {
		t.Fatalf("v1: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	changed := make(chan struct{}, 1)
	got := make(chan string, 1)
	go func() {
		if c := waitForNewConfig(ctx, path, "v1", changed); c != nil {
			got <- c.Version
		}
	}()
	for _, bad := range []string{`{"version": "v2", "proxy_qube": `, `{"version": "v2", "workloads": {"x": {"link": "nope"}}}`, valid("v1")} {
		write(bad)
		changed <- struct{}{}
		select {
		case v := <-got:
			t.Fatalf("took %q from %s", v, bad)
		case <-time.After(900 * time.Millisecond):
		}
	}
	write(valid("v2"))
	changed <- struct{}{}
	select {
	case v := <-got:
		if v != "v2" {
			t.Errorf("took %q", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the valid v2 was not taken")
	}
}
