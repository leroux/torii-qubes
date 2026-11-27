// Package admin is the program's local, read-only API on a unix socket:
// status, explain and the decision log. dom0 reaches it through the
// torii.Status qrexec service; tests and check call it directly.
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/leroux/torii-qubes/proxy/internal/engine"
	"github.com/leroux/torii-qubes/proxy/internal/events"
)

// Handler serves the API for e: GET requests only, for status, the log
// and explain.
//
//charter:realizes SW-261127-B8BM
func Handler(e *engine.Engine) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, e.Status(r.Context()))
	})
	mux.HandleFunc("GET /log", func(w http.ResponseWriter, r *http.Request) {
		ev := e.Log().Recent(r.URL.Query().Get("workload"))
		if ev == nil {
			ev = []events.Event{}
		}
		writeJSON(w, ev)
	})
	mux.HandleFunc("GET /explain", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		x, err := e.Explain(r.Context(), engine.ExplainRequest{
			Workload: q.Get("workload"), Direction: q.Get("direction"), Dest: q.Get("dest"), SNI: q.Get("sni"),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, x)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// Client talks to the API over its socket.
type Client struct{ Socket string }

func (c Client) http() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", c.Socket)
		},
	}}
}

// Get fetches path with query and decodes into out (or copies raw to out
// when it is an io.Writer).
func (c Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://torii"+path+"?"+q.Encode(), nil)
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, b)
	}
	if w, ok := out.(io.Writer); ok {
		_, err = io.Copy(w, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
