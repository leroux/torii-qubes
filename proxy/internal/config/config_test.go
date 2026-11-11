package config

import (
	"strings"
	"testing"
)

const example = `{
  "version": "v1",
  "proxy_qube": "sys-lintel",
  "state_dir": "/rw/lintel",
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
		"exit without identity":    `{"link":"routed","address":"10.0.0.1","internet":"full","exit_node":"x"}`,
		"exit without internet":    `{"link":"routed","address":"10.0.0.1","identity":{"tags":["tag:a"]},"exit_node":"x"}`,
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
