package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gen1nya/wg-admin/agent/internal/api"
	"github.com/gen1nya/wg-admin/agent/internal/devseed"
	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/plan"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// testServer spins up an api.Server backed by a fresh SQLite + mock kernel,
// preseeded with dev interfaces, marks and exits.
type testServer struct {
	srv    *api.Server
	kernel *kernel.Mock
	mux    http.Handler
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := devseed.Seed(context.Background(), st); err != nil {
		t.Fatalf("seed: %v", err)
	}
	k := kernel.NewMock()
	engine := plan.NewEngine(st, k)
	s := &api.Server{Store: st, Kernel: k, Plan: engine}
	return &testServer{srv: s, kernel: k, mux: s.Mux()}
}

func (ts *testServer) do(t *testing.T, method, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, url, rdr)
	req.Header.Set("content-type", "application/json")
	w := httptest.NewRecorder()
	ts.mux.ServeHTTP(w, req)
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(w.Body).Decode(&v); err != nil {
		t.Fatalf("decode (body=%q): %v", w.Body.String(), err)
	}
	return v
}

func TestStatus(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(t, "GET", "/status", nil)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	got := decode[map[string]any](t, w)
	if got["kernel_mode"] != "mock" {
		t.Errorf("kernel_mode=%v, want mock", got["kernel_mode"])
	}
}

func TestListInterfacesBulkIncludesStatus(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(t, "GET", "/interfaces?include=status", nil)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	list := decode[[]map[string]any](t, w)
	if len(list) < 1 {
		t.Fatalf("empty list")
	}
	// Mock kernel seeds wg0 + wg-clients-b with known pubkeys — either
	// has live status populated, since devseed re-upserts interfaces and
	// mock returns them.
	var wg0 map[string]any
	for _, it := range list {
		if it["name"] == "wg0" {
			wg0 = it
		}
	}
	if wg0 == nil {
		t.Fatal("wg0 not in bulk response")
	}
	st, ok := wg0["status"].(map[string]any)
	if !ok {
		t.Fatalf("wg0.status missing: %+v", wg0)
	}
	if st["name"] != "wg0" {
		t.Errorf("status.name=%v, want wg0", st["name"])
	}
}

func TestListInterfacesPlainShapeUnchanged(t *testing.T) {
	// Без include=status ответ — плоский массив Interface-записей
	// (без полей status/status_error). Легаси-клиенты не должны сломаться.
	ts := newTestServer(t)
	w := ts.do(t, "GET", "/interfaces", nil)
	if w.Code != 200 {
		t.Fatalf("code=%d", w.Code)
	}
	list := decode[[]map[string]any](t, w)
	for _, it := range list {
		if _, has := it["status"]; has {
			t.Errorf("plain response should not include status: %+v", it)
		}
	}
}

func TestCreateListGetPeer(t *testing.T) {
	ts := newTestServer(t)

	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "alice"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := decode[map[string]any](t, w)
	id, _ := created["id"].(float64)
	if id == 0 {
		t.Fatal("no id in response")
	}
	if got, _ := created["address"].(string); got != "10.8.1.2/32" {
		t.Errorf("auto address=%q, want 10.8.1.2/32", got)
	}
	pub, _ := created["public_key"].(string)
	if pub == "" {
		t.Fatal("no public_key")
	}

	// Kernel should have been informed.
	st, err := ts.kernel.ShowInterface("wg0")
	if err != nil {
		t.Fatalf("kernel.ShowInterface: %v", err)
	}
	found := false
	for _, p := range st.Peers {
		if p.PublicKey == pub {
			found = true
			break
		}
	}
	if !found {
		t.Error("peer not in kernel state after create")
	}

	// GET /peers
	w = ts.do(t, "GET", "/peers", nil)
	peers := decode[[]map[string]any](t, w)
	if len(peers) != 1 {
		t.Errorf("len(peers)=%d, want 1", len(peers))
	}
}

func TestCreatePeerMissingName(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{})
	if w.Code != 400 {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAutoAddressSkipsExisting(t *testing.T) {
	ts := newTestServer(t)
	// take .2 explicitly
	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{
		"name": "a", "address": "10.8.1.2/32",
	})
	if w.Code != 201 {
		t.Fatalf("first create: %d %s", w.Code, w.Body.String())
	}
	// next auto must be .3 (skip .1 iface, skip .2 taken)
	w = ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "b"})
	if w.Code != 201 {
		t.Fatalf("second create: %d %s", w.Code, w.Body.String())
	}
	got := decode[map[string]any](t, w)
	if addr, _ := got["address"].(string); addr != "10.8.1.3/32" {
		t.Errorf("second auto address=%q, want 10.8.1.3/32", addr)
	}
}

func TestPatchExitValidates(t *testing.T) {
	ts := newTestServer(t)
	// create a peer
	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "p"})
	created := decode[map[string]any](t, w)
	id := int(created["id"].(float64))

	// find the exit-a
	w = ts.do(t, "GET", "/exits", nil)
	exits := decode[[]map[string]any](t, w)
	var exitAID int
	for _, e := range exits {
		if e["name"] == "exit-a" {
			exitAID = int(e["id"].(float64))
		}
	}
	if exitAID == 0 {
		t.Fatal("exit-a not seeded")
	}

	// happy path
	w = ts.do(t, "PATCH", httpPath("/peers/%d/exit", id),
		map[string]any{"exit_id": exitAID})
	if w.Code != 200 {
		t.Fatalf("patch exit: %d %s", w.Code, w.Body.String())
	}

	// bad exit id
	w = ts.do(t, "PATCH", httpPath("/peers/%d/exit", id),
		map[string]any{"exit_id": 99999})
	if w.Code != 404 {
		t.Errorf("bad exit: want 404, got %d", w.Code)
	}

	// clear
	w = ts.do(t, "PATCH", httpPath("/peers/%d/exit", id),
		map[string]any{"clear": true})
	if w.Code != 200 {
		t.Fatalf("clear exit: %d %s", w.Code, w.Body.String())
	}
}

func TestClientConfigRendering(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "c"})
	created := decode[map[string]any](t, w)
	id := int(created["id"].(float64))

	w = ts.do(t, "GET", httpPath("/peers/%d/config", id), nil)
	if w.Code != 200 {
		t.Fatalf("config: %d %s", w.Code, w.Body.String())
	}
	got := decode[map[string]any](t, w)
	conf, _ := got["config"].(string)
	wantContains := []string{
		"[Interface]", "PrivateKey = ", "Address = 10.8.1.",
		"DNS = 8.8.8.8", "[Peer]", "PublicKey = ",
		"Endpoint = mock.example.com:51820",
		"AllowedIPs = ", "PersistentKeepalive = 25",
	}
	for _, s := range wantContains {
		if !strings.Contains(conf, s) {
			t.Errorf("config missing %q; got:\n%s", s, conf)
		}
	}
}

func TestDeletePeer(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "doomed"})
	created := decode[map[string]any](t, w)
	id := int(created["id"].(float64))
	pub, _ := created["public_key"].(string)

	w = ts.do(t, "DELETE", httpPath("/peers/%d", id), nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
	// kernel should no longer have it
	st, _ := ts.kernel.ShowInterface("wg0")
	for _, p := range st.Peers {
		if p.PublicKey == pub {
			t.Error("kernel still has deleted peer")
		}
	}
	// get returns 404
	w = ts.do(t, "GET", httpPath("/peers/%d", id), nil)
	if w.Code != 404 {
		t.Errorf("get after delete: want 404, got %d", w.Code)
	}
}

func TestTrafficJoinsKernelWithDB(t *testing.T) {
	ts := newTestServer(t)
	// create one peer
	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "t"})
	created := decode[map[string]any](t, w)
	pub, _ := created["public_key"].(string)

	// traffic should list at least this peer + the seeded mock peer (unknown)
	w = ts.do(t, "GET", "/traffic", nil)
	if w.Code != 200 {
		t.Fatalf("traffic: %d", w.Code)
	}
	entries := decode[[]map[string]any](t, w)
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	var foundOurs, foundUnknown bool
	for _, e := range entries {
		if e["public_key"] == pub {
			foundOurs = true
			if e["peer_name"] != "t" {
				t.Errorf("joined peer_name=%v, want t", e["peer_name"])
			}
		}
		if unk, _ := e["unknown"].(bool); unk {
			foundUnknown = true
		}
	}
	if !foundOurs {
		t.Error("our peer not in traffic list")
	}
	if !foundUnknown {
		t.Error("preseeded mock peer should be flagged unknown=true")
	}
}

// TestMeshRoleGuards verifies tier-1 peer CRUD refuses to touch an interface
// whose role is 'mesh' — a mesh tunnel isn't a place to register clients,
// and accidentally doing so would inject a stray peer alongside the real
// remote endpoint.
func TestMeshRoleGuards(t *testing.T) {
	ts := newTestServer(t)

	// Insert a mesh-role interface directly; devseed only creates client ones.
	ctx := context.Background()
	meshIface := &model.Interface{
		Name: "wg-mesh-test", Address: "10.99.0.2/30", Subnet: "10.99.0.0/30",
		ListenPort: 51999, PrivateKey: "X" + strings.Repeat("A", 43), // 44-char stub
		PublicEndpoint: "127.0.0.1", PublicPort: 51999,
		Keepalive: 25, Role: model.RoleMesh, Enabled: true, CreatedAt: 1,
	}
	meshID, err := ts.srv.Store.UpsertInterface(ctx, meshIface)
	if err != nil {
		t.Fatalf("seed mesh iface: %v", err)
	}

	// POST peer → 409
	w := ts.do(t, "POST", "/interfaces/wg-mesh-test/peers", map[string]any{"name": "stray"})
	if w.Code != 409 {
		t.Errorf("POST to mesh: want 409, got %d %s", w.Code, w.Body.String())
	}

	// Insert a peer directly (simulating import) so we can test delete/config guards.
	strayPeer := &model.Peer{
		InterfaceID: meshID, Name: "remote-exit-a",
		PublicKey:  "A" + strings.Repeat("A", 43),
		PrivateKey: "",
		Address:    "0.0.0.0/0",
		Enabled:    true, Tags: "[]", CreatedAt: 1,
	}
	peerID, err := ts.srv.Store.InsertPeer(ctx, strayPeer)
	if err != nil {
		t.Fatalf("seed mesh peer: %v", err)
	}

	// DELETE peer → 409
	w = ts.do(t, "DELETE", httpPath("/peers/%d", int(peerID)), nil)
	if w.Code != 409 {
		t.Errorf("DELETE mesh peer: want 409, got %d %s", w.Code, w.Body.String())
	}

	// GET /peers/{id}/config → 409
	w = ts.do(t, "GET", httpPath("/peers/%d/config", int(peerID)), nil)
	if w.Code != 409 {
		t.Errorf("GET mesh config: want 409, got %d %s", w.Code, w.Body.String())
	}
}

// TestPlanCreateApplyConfirm drives the full tier-2 flow against the mock
// kernel: desired state → plan (diff + snapshot) → apply → confirm.
func TestPlanCreateApplyConfirm(t *testing.T) {
	ts := newTestServer(t)
	// devseed creates ipsets "direct" and "telegram-dc" — override with a
	// known initial state.
	_ = ts.kernel.IPSetReplace("direct", []string{"77.88.0.0/16"})

	// POST /plan with two sets: one update, one new.
	w := ts.do(t, "POST", "/plan", map[string]any{
		"description": "test",
		"desired": map[string]any{
			"ipsets": []map[string]any{
				{"name": "direct", "entries": []string{"77.88.0.0/16", "87.250.0.0/16"}},
				{"name": "fresh", "entries": []string{"1.2.3.0/24"}},
			},
		},
	})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	resp := decode[map[string]any](t, w)
	planObj := resp["plan"].(map[string]any)
	id := int(planObj["id"].(float64))
	if planObj["state"] != "pending" {
		t.Errorf("state=%v", planObj["state"])
	}
	diff := resp["diff"].(map[string]any)
	if diff["ipsets"] == nil {
		t.Fatal("diff missing ipsets")
	}

	// POST /plans/{id}/apply
	w = ts.do(t, "POST", httpPath("/plans/%d/apply?timeout=30", id), nil)
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	applied := decode[map[string]any](t, w)
	if applied["state"] != "applied" {
		t.Errorf("state=%v", applied["state"])
	}

	// Kernel should reflect desired state.
	direct, _ := ts.kernel.IPSetList("direct")
	if len(direct) != 2 {
		t.Errorf("direct after apply=%v", direct)
	}
	fresh, err := ts.kernel.IPSetList("fresh")
	if err != nil || len(fresh) != 1 {
		t.Errorf("fresh after apply: entries=%v err=%v", fresh, err)
	}

	// POST /plans/{id}/confirm
	w = ts.do(t, "POST", httpPath("/plans/%d/confirm", id), nil)
	if w.Code != 200 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	confirmed := decode[map[string]any](t, w)
	if confirmed["state"] != "confirmed" {
		t.Errorf("state=%v", confirmed["state"])
	}
}

// TestPlanManualRevert: apply then revert without waiting for watchdog.
func TestPlanManualRevert(t *testing.T) {
	ts := newTestServer(t)
	_ = ts.kernel.IPSetReplace("direct", []string{"77.88.0.0/16"})

	w := ts.do(t, "POST", "/plan", map[string]any{
		"desired": map[string]any{
			"ipsets": []map[string]any{
				{"name": "direct", "entries": []string{"99.99.0.0/16"}},
			},
		},
	})
	planObj := decode[map[string]any](t, w)["plan"].(map[string]any)
	id := int(planObj["id"].(float64))

	w = ts.do(t, "POST", httpPath("/plans/%d/apply", id), nil)
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}

	w = ts.do(t, "POST", httpPath("/plans/%d/revert", id), nil)
	if w.Code != 200 {
		t.Fatalf("revert: %d %s", w.Code, w.Body.String())
	}
	reverted := decode[map[string]any](t, w)
	if reverted["state"] != "reverted" {
		t.Errorf("state=%v", reverted["state"])
	}

	// Kernel should be back to the original.
	entries, _ := ts.kernel.IPSetList("direct")
	if len(entries) != 1 || entries[0] != "77.88.0.0/16" {
		t.Errorf("kernel after revert=%v", entries)
	}
}

// TestPlanFullStack drives ipsets + rules + routes + nft through a single
// POST /plan → apply → confirm, verifying each resource lands in the mock
// kernel. Exercises the API layer wiring for phase 2+3.
func TestPlanFullStack(t *testing.T) {
	ts := newTestServer(t)
	// devseed seeds a mark pointing routing_table=wg_tunnel — so routes to
	// that table are allowed. Verify that.
	marks, _ := ts.srv.Store.ListMarks(context.Background())
	found := false
	for _, m := range marks {
		if m.RoutingTable == "wg_tunnel" {
			found = true
		}
	}
	if !found {
		t.Fatal("devseed should register wg_tunnel mark")
	}

	body := map[string]any{
		"description": "full-stack smoke",
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
			"nft": map[string]any{
				"body": "chain forward { type filter hook forward priority 0; policy accept; }",
			},
		},
	}
	w := ts.do(t, "POST", "/plan", body)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	p := decode[map[string]any](t, w)["plan"].(map[string]any)
	id := int(p["id"].(float64))

	w = ts.do(t, "POST", httpPath("/plans/%d/apply?timeout=30", id), nil)
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}

	// Kernel reflection:
	if entries, _ := ts.kernel.IPSetList("direct"); len(entries) != 1 {
		t.Errorf("ipset direct=%v", entries)
	}
	if rules, _ := ts.kernel.RuleList(); len(rules) != 1 || rules[0].Priority != 10500 {
		t.Errorf("rules=%+v", rules)
	}
	if routes, _ := ts.kernel.RouteList("wg_tunnel"); len(routes) != 1 {
		t.Errorf("routes=%+v", routes)
	}
	if last := ts.kernel.NFT["_last_applied"]; !strings.Contains(last, "chain forward") {
		t.Errorf("nft last apply missing chain:\n%s", last)
	}

	// Confirm (cancels watchdog).
	w = ts.do(t, "POST", httpPath("/plans/%d/confirm", id), nil)
	if w.Code != 200 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
}

// TestPlanOwnershipRejection: routes to a non-owned table must fail at Create
// with 500 (not 404/409 — ownership is a validation failure distinct from
// state-machine conflicts).
func TestPlanOwnershipRejection(t *testing.T) {
	ts := newTestServer(t)
	body := map[string]any{
		"desired": map[string]any{
			"routes": []map[string]any{
				{"table": "main", "dest": "default", "dev": "wg0"},
			},
		},
	}
	w := ts.do(t, "POST", "/plan", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "forbidden") {
		t.Errorf("expected 'forbidden' in error, got: %s", w.Body.String())
	}
}

// TestPlanConflictOnDoubleApply: a second plan applied while another is in
// 'applied' state returns 409.
func TestPlanConflictOnDoubleApply(t *testing.T) {
	ts := newTestServer(t)

	body := map[string]any{
		"desired": map[string]any{
			"ipsets": []map[string]any{{"name": "a", "entries": []string{"1.0.0.0/24"}}},
		},
	}
	w := ts.do(t, "POST", "/plan", body)
	p1 := decode[map[string]any](t, w)["plan"].(map[string]any)
	id1 := int(p1["id"].(float64))

	w = ts.do(t, "POST", "/plan", body)
	p2 := decode[map[string]any](t, w)["plan"].(map[string]any)
	id2 := int(p2["id"].(float64))

	w = ts.do(t, "POST", httpPath("/plans/%d/apply", id1), nil)
	if w.Code != 200 {
		t.Fatalf("apply p1: %d", w.Code)
	}
	w = ts.do(t, "POST", httpPath("/plans/%d/apply", id2), nil)
	if w.Code != 409 {
		t.Errorf("apply p2: want 409, got %d %s", w.Code, w.Body.String())
	}
}

func httpPath(tmpl string, args ...any) string {
	// tiny helper to avoid importing fmt everywhere
	s := tmpl
	for _, a := range args {
		var v string
		switch x := a.(type) {
		case int:
			v = itoa(x)
		default:
			v = ""
		}
		idx := strings.Index(s, "%d")
		if idx < 0 {
			break
		}
		s = s[:idx] + v + s[idx+2:]
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
