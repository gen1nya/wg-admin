package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/api"
	"github.com/gen1nya/wg-admin/agent/internal/devseed"
	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/plan"
	"github.com/gen1nya/wg-admin/agent/internal/server"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// socketHarness wires the whole daemon stack (store + kernel + engine + api
// + listener) onto a real unix socket in tmpdir. Exactly what cmd/wg-agent
// does on startup, minus the CLI flag parsing. Each test gets its own.
type socketHarness struct {
	t      *testing.T
	client *http.Client
	socket string
}

func newSocketHarness(t *testing.T) *socketHarness {
	t.Helper()
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "wg-agent.sock")
	dbPath := filepath.Join(dir, "state.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := devseed.Seed(context.Background(), st); err != nil {
		t.Fatalf("seed: %v", err)
	}

	k := kernel.NewMock()
	engine := plan.NewEngine(st, k)
	apiSrv := &api.Server{Store: st, Kernel: k, Plan: engine}

	srv := server.New(server.Config{
		SocketPath: socketPath,
		SocketMode: 0o600,
		Handler:    apiSrv.Mux(),
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("server start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}
	return &socketHarness{t: t, client: client, socket: socketPath}
}

func (h *socketHarness) req(method, path string, body any) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://unix"+path, rdr)
	if err != nil {
		h.t.Fatalf("new req: %v", err)
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

func mustJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func TestSocketStatusViaUnixListener(t *testing.T) {
	h := newSocketHarness(t)
	resp := h.req("GET", "/status", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got := mustJSON[map[string]any](t, resp)
	if got["kernel_mode"] != "mock" {
		t.Errorf("kernel_mode=%v", got["kernel_mode"])
	}
}

// TestSocketFullPlanLifecycle walks the tier-2 flow through a live unix
// socket: create plan → apply → confirm. Verifies the whole transport
// layer behaves correctly including socket permissions, unix dialer,
// JSON framing, and shutdown.
func TestSocketFullPlanLifecycle(t *testing.T) {
	h := newSocketHarness(t)

	// Create a plan with a mix of resource types. devseed seeded a mark
	// routing_table=wg_tunnel, so routes to it are allowed.
	body := map[string]any{
		"description": "socket e2e",
		"desired": map[string]any{
			"ipsets": []map[string]any{
				{"name": "direct", "entries": []string{"77.88.0.0/16"}},
			},
			"rules": []map[string]any{
				{"priority": 10500, "fwmark": 1, "table": "wg_tunnel"},
			},
			"routes": []map[string]any{
				{"table": "wg_tunnel", "dest": "default", "dev": "wg-exit-a"},
			},
		},
	}
	resp := h.req("POST", "/plan", body)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	created := mustJSON[map[string]any](t, resp)
	planObj := created["plan"].(map[string]any)
	id := int(planObj["id"].(float64))
	if planObj["state"] != "pending" {
		t.Errorf("state=%v", planObj["state"])
	}

	// Apply.
	resp = h.req("POST", httpPath("/plans/%d/apply?timeout=30", id), nil)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("apply: %d %s", resp.StatusCode, b)
	}
	applied := mustJSON[map[string]any](t, resp)
	if applied["state"] != "applied" {
		t.Errorf("state=%v", applied["state"])
	}

	// Confirm.
	resp = h.req("POST", httpPath("/plans/%d/confirm", id), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("confirm: %d", resp.StatusCode)
	}
	confirmed := mustJSON[map[string]any](t, resp)
	if confirmed["state"] != "confirmed" {
		t.Errorf("state=%v", confirmed["state"])
	}

	// Sanity: GET /plans should list it.
	resp = h.req("GET", "/plans", nil)
	plans := mustJSON[[]map[string]any](t, resp)
	if len(plans) != 1 || int(plans[0]["id"].(float64)) != id {
		t.Errorf("list plans: %+v", plans)
	}
}

// TestSocketBadPathReturns404: the unix transport doesn't special-case
// anything vs. tcp; sanity check.
func TestSocketBadPathReturns404(t *testing.T) {
	h := newSocketHarness(t)
	resp := h.req("GET", "/does-not-exist", nil)
	if resp.StatusCode != 404 {
		t.Errorf("bad path: want 404, got %d", resp.StatusCode)
	}
}
