package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/leroux/torii-qubes/claims"
	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/tailnet"
)

// Each identity in a config is a node of its own: its own hostname and
// its own state directory.
func TestOneNodePerIdentity(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_WNGN, "one-node-each")
	cfg, err := config.Parse([]byte(`{"proxy_qube":"p","state_dir":"/rw/torii/state","workloads":{
	  "a": {"link":"qrexec","identity":{"hostname":"m1-a","tags":["tag:m1-x"]}},
	  "b": {"link":"qrexec","identity":{"hostname":"m1-b","tags":["tag:m1-x"]}},
	  "c": {"link":"qrexec","internet":"full","socks":true}},
	  "egresses":{"vpn":{"hostname":"m1-egress-vpn","tags":["tag:m1-egress"],"exits":["x"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	ids, err := Identities(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"a":          {"m1-a", "/rw/torii/state/a"},
		"b":          {"m1-b", "/rw/torii/state/b"},
		"egress:vpn": {"m1-egress-vpn", "/rw/torii/state/egress-vpn"},
	}
	if len(ids) != len(want) {
		t.Fatalf("%d identities, want %d: %v", len(ids), len(want), ids)
	}
	for k, w := range want {
		ts, ok := ids[k].(*tailnet.TSNet)
		if !ok {
			t.Fatalf("%s: %T", k, ids[k])
		}
		if ts.Hostname() != w[0] || filepath.ToSlash(ts.StateDir()) != w[1] {
			t.Errorf("%s: %s in %s, want %s in %s", k, ts.Hostname(), ts.StateDir(), w[0], w[1])
		}
	}
}

// A workload named like an egress's state is refused before any node is
// built, so no two identities share a state directory or a key.
func TestAWorkloadCannotTakeAnEgresssState(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_WNGN, "one-node-each")
	_, err := config.Parse([]byte(`{"proxy_qube":"p","state_dir":"/s","workloads":{
	  "egress-vpn": {"link":"qrexec","identity":{"hostname":"other","tags":["tag:m1-x"]}}},
	  "egresses":{"vpn":{"hostname":"m1-egress-vpn","tags":["tag:m1-egress"],"exits":["x"]}}}`))
	if err == nil || !strings.Contains(err.Error(), "share a state directory") {
		t.Errorf("err = %v", err)
	}
}
