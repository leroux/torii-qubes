package config

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/leroux/torii-qubes/claims"
)

const example = `{
  "version": "v1",
  "proxy_qube": "sys-torii",
  "state_dir": "/rw/torii",
  "workloads": {
    "infra": {"link": "qrexec", "identity": {"tags": ["tag:server"]}, "inbound": [{"port": 443}]},
    "job-1": {"link": "qrexec", "identity": {"tags": ["tag:job"]}, "inbound": [{"port": 8080, "target": 80}],
              "tailnet_outbound": true, "internet": "allowlist", "internet_allow": ["PyPI.org", "*.github.com"],
              "socks": true, "forwards": [{"name": "db", "to": "store:8000"}]},
    "store": {"link": "routed", "address": "10.137.0.12", "identity": {"tags": ["tag:server"]}, "inbound": [{"port": 8000}], "internet": "full"}
  }
}`

func TestParseExample(t *testing.T) {
	c, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Workloads["infra"].Identity.Hostname; got != "infra" {
		t.Errorf("hostname defaults to the qube name, got %q", got)
	}
	if got := c.Workloads["job-1"].InternetAllow[0]; got != "pypi.org" {
		t.Errorf("allowlist is lowercased, got %q", got)
	}
	if got := c.Workloads["job-1"].Inbound[0].TargetPort(); got != 80 {
		t.Errorf("target port %d", got)
	}
	if c.Listen.Intercept != DefaultIntercept || c.ExitDNS != DefaultExitDNS {
		t.Errorf("defaults not filled: %+v", c.Listen)
	}
	if n, _, ok := c.ByAddress(mustAddr("10.137.0.12")); !ok || n != "store" {
		t.Errorf("ByAddress = %q %v", n, ok)
	}
}

func TestParseRefuses(t *testing.T) {
	base := `{"proxy_qube":"p","state_dir":"/s","workloads":{"w":%s}}`
	cases := map[string]string{
		"unknown field":            `{"link":"qrexec","netvm":"x"}`,
		"bad link":                 `{"link":"bridge"}`,
		"routed without address":   `{"link":"routed"}`,
		"address on qrexec":        `{"link":"qrexec","address":"10.0.0.1"}`,
		"allowlist without hosts":  `{"link":"routed","address":"10.0.0.1","internet":"allowlist"}`,
		"hosts without allowlist":  `{"link":"routed","address":"10.0.0.1","internet":"full","internet_allow":["a.com"]}`,
		"bad pattern":              `{"link":"routed","address":"10.0.0.1","internet":"allowlist","internet_allow":["a.*.com"]}`,
		"inbound without identity": `{"link":"qrexec","inbound":[{"port":1}]}`,
		"outbound w/o identity":    `{"link":"qrexec","tailnet_outbound":true}`,
		"exit_node is gone":        `{"link":"routed","address":"10.0.0.1","internet":"full","exit_node":"x"}`,
		"egress not defined":       `{"link":"routed","address":"10.0.0.1","internet":"full","egress":"vpn"}`,
		"untagged non-login":       `{"link":"qrexec","identity":{}}`,
		"tagged login":             `{"link":"qrexec","identity":{"login":true,"tags":["tag:a"]}}`,
		"bad tag":                  `{"link":"qrexec","identity":{"tags":["server"]}}`,
		"port twice":               `{"link":"qrexec","identity":{"tags":["tag:a"]},"inbound":[{"port":1},{"port":1}]}`,
		"socks on routed":          `{"link":"routed","address":"10.0.0.1","socks":true}`,
		"none is not the proxy":    `{"link":"none","internet":"full"}`,
		"qrexec forward listens":   `{"link":"qrexec","forwards":[{"name":"a","to":"x:1","listen":9}]}`,
		"bad forward":              `{"link":"qrexec","forwards":[{"name":"a b","to":"x:1"}]}`,
		"forward without port":     `{"link":"qrexec","forwards":[{"name":"a","to":"x"}]}`,
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(strings.Replace(base, "%s", w, 1))); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := Parse([]byte(`{"proxy_qube":"p","state_dir":"/s","workloads":{
		"a":{"link":"routed","address":"10.0.0.1"},"b":{"link":"routed","address":"10.0.0.1"}}}`)); err == nil {
		t.Error("shared address accepted")
	}
	if _, err := Parse([]byte(`{"proxy_qube":"p","state_dir":"/s","workloads":{
		"a":{"link":"qrexec","identity":{"hostname":"x","tags":["tag:a"]}},"b":{"link":"qrexec","identity":{"hostname":"X","tags":["tag:a"]}}}}`)); err == nil {
		t.Error("shared hostname accepted")
	}
}

func TestProxyQubeItself(t *testing.T) {
	ok := `{"proxy_qube":"p","state_dir":"/s","workloads":{"p":{"link":"none","socks":true,"internet":"full",
		"forwards":[{"name":"a","to":"x:1","listen":9000},{"name":"b","to":"y:2","listen":9001}]}}}`
	c, err := Parse([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.LocalSOCKS != DefaultLocalSOCKS || c.Listen.UDP != DefaultUDP {
		t.Errorf("%+v", c.Listen)
	}
	for name, w := range map[string]string{
		"forward without listen": `{"link":"none","forwards":[{"name":"a","to":"x:1"}]}`,
		"listen port twice":      `{"link":"none","forwards":[{"name":"a","to":"x:1","listen":9},{"name":"b","to":"x:1","listen":9}]}`,
	} {
		if _, err := Parse([]byte(`{"proxy_qube":"p","state_dir":"/s","workloads":{"p":` + w + `}}}`)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

const egressConfig = `{
  "proxy_qube": "sys-torii", "state_dir": "/s",
  "egresses": {"vpn": {"hostname": "m1-egress-vpn", "tags": ["tag:m1-egress"], "exits": ["exit-1", "exit-2"]}},
  "unlisted": {"internet": "full", "egress": "vpn"},
  "workloads": {
    "browser": {"link": "routed", "address": "10.137.0.20", "internet": "full", "egress": "vpn"}
  }
}`

func TestEgressAndUnlisted(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_BNFC, "default-two-minutes")
	c, err := Parse([]byte(egressConfig))
	if err != nil {
		t.Fatal(err)
	}
	if g := c.Egresses["vpn"]; g.FailbackAfter != 120 {
		t.Errorf("failback default: %d", g.FailbackAfter)
	}
	// An egress workload needs no identity of its own.
	if w := c.Workloads["browser"]; w.Identity != nil || c.EgressOf(w).Hostname != "m1-egress-vpn" {
		t.Errorf("browser: %+v", w)
	}
	// A listed address is its workload; any other is unlisted.
	if n, _, ok := c.ByAddress(mustAddr("10.137.0.20")); !ok || n != "browser" {
		t.Errorf("listed: %q", n)
	}
	n, w, ok := c.ByAddress(mustAddr("10.137.0.99"))
	if !ok || n != "unlisted:10.137.0.99" || w.Identity != nil || w.Egress != "vpn" || w.Internet != InternetFull {
		t.Errorf("unlisted: %q %+v", n, w)
	}
	if lw, ok := c.Lookup(n); !ok || lw.Address != "10.137.0.99" {
		t.Errorf("Lookup(%s) = %+v", n, lw)
	}
	// Without Unlisted, an unknown address is nobody.
	c.Unlisted = nil
	if _, _, ok := c.ByAddress(mustAddr("10.137.0.99")); ok {
		t.Error("unlisted without config")
	}
	if _, ok := c.Lookup("unlisted:10.137.0.99"); ok {
		t.Error("Lookup unlisted without config")
	}
}

func TestEgressRefuses(t *testing.T) {
	for name, tc := range map[string]string{
		"no exits":           `"egresses": {"vpn": {"hostname": "h", "tags": ["tag:e"], "exits": []}}`,
		"no tags":            `"egresses": {"vpn": {"hostname": "h", "exits": ["x"]}}`,
		"bad probe":          `"egresses": {"vpn": {"hostname": "h", "tags": ["tag:e"], "exits": ["x"], "probe": "nope"}}`,
		"unlisted no egress": `"unlisted": {"internet": "full", "egress": "nope"}`,
		"unlisted none":      `"unlisted": {"internet": "none"}`,
		"egress hostname twice": `"egresses": {"vpn": {"hostname": "w", "tags": ["tag:e"], "exits": ["x"]}},
		                          "workloads": {"w": {"link": "qrexec", "identity": {"hostname": "w", "tags": ["tag:a"]}}}`,
	} {
		body := `{"proxy_qube": "p", "state_dir": "/s", ` + tc + `}`
		if !strings.Contains(tc, `"workloads"`) {
			body = `{"proxy_qube": "p", "state_dir": "/s", "workloads": {}, ` + tc + `}`
		}
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A routed source is named by its Qubes address; an address no entry has
// is the unlisted source only when the proxy has an unlisted policy.
func TestByAddressNamesTheRoutedQube(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_BHHX, "source-names-qube", "unknown-address-unlisted")
	src := `{"proxy_qube":"p","state_dir":"/s","workloads":{
	  "browser": {"link":"routed","address":"10.137.0.20","internet":"full"},
	  "job": {"link":"qrexec","internet":"full","socks":true}}%s}`
	c, err := Parse([]byte(fmt.Sprintf(src, "")))
	if err != nil {
		t.Fatal(err)
	}
	if n, w, ok := c.ByAddress(netip.MustParseAddr("10.137.0.20")); !ok || n != "browser" || w != c.Workloads["browser"] {
		t.Errorf("10.137.0.20 = %q %v", n, ok)
	}
	if n, _, ok := c.ByAddress(netip.MustParseAddr("::ffff:10.137.0.20")); !ok || n != "browser" {
		t.Errorf("v4-mapped = %q %v", n, ok)
	}
	if n, _, ok := c.ByAddress(netip.MustParseAddr("10.137.0.99")); ok {
		t.Errorf("an unknown address named %q with no unlisted policy", n)
	}
	c, err = Parse([]byte(fmt.Sprintf(src, `, "unlisted": {"internet": "full"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if n, _, ok := c.ByAddress(netip.MustParseAddr("10.137.0.99")); !ok || n != "unlisted:10.137.0.99" {
		t.Errorf("with unlisted: %q %v", n, ok)
	}
}
