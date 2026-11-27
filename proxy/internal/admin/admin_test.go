package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leroux/torii-qubes/claims"
	"github.com/leroux/torii-qubes/proxy/internal/config"
	"github.com/leroux/torii-qubes/proxy/internal/engine"
)

// The local API reads; nothing it answers changes the proxy.
func TestTheAPIOnlyReads(t *testing.T) {
	claims.Verifies(t, claims.SW_261127_B8BM, "api-only-reads")
	cfg, err := config.Parse([]byte(`{"proxy_qube":"p","state_dir":"/s","workloads":{"job":{"link":"qrexec","internet":"full","socks":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(engine.New(engine.Deps{Config: cfg})))
	defer srv.Close()
	for _, path := range []string{"/status", "/log", "/explain?workload=job&dest=example.com:443"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: %d", path, resp.StatusCode)
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			req, _ := http.NewRequest(method, srv.URL+strings.Split(path, "?")[0], strings.NewReader("{}"))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: %d, want refused", method, path, resp.StatusCode)
			}
		}
	}
	for _, path := range []string{"/config", "/reload", "/rules", "/"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d, want not found", path, resp.StatusCode)
		}
	}
}
