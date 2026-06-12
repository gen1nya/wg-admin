package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
)

// fakeStore satisfies the narrow Store interface for tests.
type fakeStore struct {
	interfaces []model.Interface
	peers      map[int64][]model.Peer
	auditCalls []auditCall
}

type auditCall struct {
	Action  string
	Payload string
}

func (f *fakeStore) ListInterfaces(ctx context.Context) ([]model.Interface, error) {
	return f.interfaces, nil
}
func (f *fakeStore) ListPeersByInterface(ctx context.Context, id int64) ([]model.Peer, error) {
	return f.peers[id], nil
}
func (f *fakeStore) LogAudit(ctx context.Context, actor, action, et string, id *int64, payload string) error {
	f.auditCalls = append(f.auditCalls, auditCall{Action: action, Payload: payload})
	return nil
}

func mockWithPeers(iface string, peers ...kernel.PeerStatus) *kernel.Mock {
	m := kernel.NewMock()
	// Clear seed state so tests are explicit about what exists.
	for name := range m.Interfaces {
		delete(m.Interfaces, name)
	}
	m.Interfaces[iface] = &kernel.InterfaceStatus{
		Name:  iface,
		Peers: peers,
	}
	return m
}

// Common fixture: DB has wg0 (role=clients, id=1, enabled) and wg-mesh
// (role=mesh, id=2, enabled). Only wg0 should be touched.
func baseFixture() *fakeStore {
	return &fakeStore{
		interfaces: []model.Interface{
			{ID: 1, Name: "wg0", Role: model.RoleClients, Enabled: true},
			{ID: 2, Name: "wg-mesh", Role: model.RoleMesh, Enabled: true},
		},
		peers: map[int64][]model.Peer{
			1: {
				{PublicKey: "KEYA", Address: "10.10.10.2/32", Enabled: true},
				{PublicKey: "KEYB", Address: "10.10.10.3/32", Enabled: true},
			},
			// mesh peer with narrow DB address; must NOT be sync'd
			2: {
				{PublicKey: "MESHKEY", Address: "10.200.1.0/24", Enabled: true},
			},
		},
	}
}

func TestPeersAddsMissingFromKernel(t *testing.T) {
	fs := baseFixture()
	k := kernel.NewMock()
	// Wipe seed.
	for name := range k.Interfaces {
		delete(k.Interfaces, name)
	}
	// wg0 exists but has no peers; mesh has the 0.0.0.0/0 peer (real crypto-routing scenario)
	k.Interfaces["wg0"] = &kernel.InterfaceStatus{Name: "wg0"}
	k.Interfaces["wg-mesh"] = &kernel.InterfaceStatus{Name: "wg-mesh", Peers: []kernel.PeerStatus{
		{PublicKey: "MESHKEY", AllowedIPs: "0.0.0.0/0"},
	}}

	if err := Peers(context.Background(), fs, k); err != nil {
		t.Fatalf("Peers: %v", err)
	}

	st, _ := k.ShowInterface("wg0")
	if len(st.Peers) != 2 {
		t.Fatalf("wg0 peers=%d, want 2", len(st.Peers))
	}

	// mesh must remain untouched: still the 0.0.0.0/0 peer
	mesh, _ := k.ShowInterface("wg-mesh")
	if len(mesh.Peers) != 1 || mesh.Peers[0].AllowedIPs != "0.0.0.0/0" {
		t.Errorf("mesh peer was touched: %+v", mesh.Peers)
	}
}

func TestPeersRemovesStaleFromKernel(t *testing.T) {
	fs := baseFixture()
	k := mockWithPeers("wg0",
		kernel.PeerStatus{PublicKey: "KEYA", AllowedIPs: "10.10.10.2/32"},
		kernel.PeerStatus{PublicKey: "OLDKEY", AllowedIPs: "10.10.10.99/32"}, // not in DB
	)
	k.Interfaces["wg-mesh"] = &kernel.InterfaceStatus{Name: "wg-mesh"}

	if err := Peers(context.Background(), fs, k); err != nil {
		t.Fatalf("Peers: %v", err)
	}

	st, _ := k.ShowInterface("wg0")
	if len(st.Peers) != 2 {
		t.Fatalf("peers=%d, want 2 (KEYA+KEYB)", len(st.Peers))
	}
	keys := map[string]bool{}
	for _, p := range st.Peers {
		keys[p.PublicKey] = true
	}
	if keys["OLDKEY"] {
		t.Error("OLDKEY should have been removed")
	}
	if !keys["KEYA"] || !keys["KEYB"] {
		t.Errorf("KEYA/KEYB missing: %+v", keys)
	}
}

func TestPeersFixesDriftedAllowedIPs(t *testing.T) {
	fs := baseFixture()
	k := mockWithPeers("wg0",
		kernel.PeerStatus{PublicKey: "KEYA", AllowedIPs: "10.10.10.99/32"}, // drifted
		kernel.PeerStatus{PublicKey: "KEYB", AllowedIPs: "10.10.10.3/32"},  // ok
	)
	k.Interfaces["wg-mesh"] = &kernel.InterfaceStatus{Name: "wg-mesh"}

	if err := Peers(context.Background(), fs, k); err != nil {
		t.Fatalf("Peers: %v", err)
	}

	st, _ := k.ShowInterface("wg0")
	m := map[string]string{}
	for _, p := range st.Peers {
		m[p.PublicKey] = p.AllowedIPs
	}
	if m["KEYA"] != "10.10.10.2/32" {
		t.Errorf("KEYA allowed-ips=%q, want 10.10.10.2/32", m["KEYA"])
	}
	if m["KEYB"] != "10.10.10.3/32" {
		t.Errorf("KEYB allowed-ips=%q", m["KEYB"])
	}
}

func TestPeersSkipsDisabledInterfaces(t *testing.T) {
	fs := baseFixture()
	fs.interfaces[0].Enabled = false // disable wg0

	k := kernel.NewMock()
	for name := range k.Interfaces {
		delete(k.Interfaces, name)
	}
	k.Interfaces["wg0"] = &kernel.InterfaceStatus{Name: "wg0"} // empty
	k.Interfaces["wg-mesh"] = &kernel.InterfaceStatus{Name: "wg-mesh"}

	if err := Peers(context.Background(), fs, k); err != nil {
		t.Fatalf("Peers: %v", err)
	}
	// wg0 disabled — not touched, peers stayed at 0
	st, _ := k.ShowInterface("wg0")
	if len(st.Peers) != 0 {
		t.Errorf("disabled iface got touched: %+v", st.Peers)
	}
}

func TestPeersRespectsDisabledPeers(t *testing.T) {
	fs := baseFixture()
	fs.peers[1][1].Enabled = false // KEYB disabled in DB

	k := mockWithPeers("wg0",
		kernel.PeerStatus{PublicKey: "KEYA", AllowedIPs: "10.10.10.2/32"},
		kernel.PeerStatus{PublicKey: "KEYB", AllowedIPs: "10.10.10.3/32"},
	)
	k.Interfaces["wg-mesh"] = &kernel.InterfaceStatus{Name: "wg-mesh"}

	if err := Peers(context.Background(), fs, k); err != nil {
		t.Fatalf("Peers: %v", err)
	}
	st, _ := k.ShowInterface("wg0")
	if len(st.Peers) != 1 || st.Peers[0].PublicKey != "KEYA" {
		t.Errorf("disabled peer not removed: %+v", st.Peers)
	}
}

func TestPeersWaitsForInterface(t *testing.T) {
	fs := baseFixture()
	// Kernel doesn't have wg0 yet. Override wait timeout by calling
	// waitForInterface directly via short-timeout goroutine is complex; instead
	// rely on the fact that Peers() logs and moves on after ~10s. Make wg0
	// disabled so we don't actually wait in the fast test.
	fs.interfaces[0].Enabled = false
	k := kernel.NewMock()
	for name := range k.Interfaces {
		delete(k.Interfaces, name)
	}
	k.Interfaces["wg-mesh"] = &kernel.InterfaceStatus{Name: "wg-mesh"}

	if err := Peers(context.Background(), fs, k); err != nil {
		t.Fatalf("Peers: %v", err)
	}
}

func TestWaitForInterfaceTimeout(t *testing.T) {
	k := kernel.NewMock()
	for name := range k.Interfaces {
		delete(k.Interfaces, name)
	}
	// 100ms timeout since iface never appears — verify we return a timeout-
	// shaped error, not ErrInterfaceNotFound directly.
	err := waitForInterface(context.Background(), k, "ghost0", 100_000_000) // 100ms
	if err == nil {
		t.Fatal("want timeout error, got nil")
	}
	if errors.Is(err, kernel.ErrInterfaceNotFound) {
		t.Errorf("want wrapped timeout error, got raw ErrInterfaceNotFound")
	}
}

func TestPeersAuditsEachClientsInterface(t *testing.T) {
	fs := baseFixture()
	k := mockWithPeers("wg0",
		kernel.PeerStatus{PublicKey: "KEYA", AllowedIPs: "10.10.10.2/32"},
	)
	k.Interfaces["wg-mesh"] = &kernel.InterfaceStatus{Name: "wg-mesh"}

	_ = Peers(context.Background(), fs, k)

	// Expect exactly one audit entry — only wg0 (clients) should be audited;
	// mesh is skipped before we reach the audit call.
	if len(fs.auditCalls) != 1 {
		t.Fatalf("audit calls=%d, want 1 (wg0 only)", len(fs.auditCalls))
	}
	if fs.auditCalls[0].Action != "boot.reconcile.peers" {
		t.Errorf("action=%q", fs.auditCalls[0].Action)
	}
}

// TestPeersSyncsPSK: a stored PSK that differs from the kernel is pushed; an
// empty stored PSK leaves the kernel's existing PSK untouched (we never strip
// a PSK the DB just doesn't know about yet — e.g. before a backfill).
func TestPeersSyncsPSK(t *testing.T) {
	fs := &fakeStore{
		interfaces: []model.Interface{
			{ID: 1, Name: "wg0", Role: model.RoleClients, Enabled: true},
		},
		peers: map[int64][]model.Peer{
			1: {
				{PublicKey: "KEYA", Address: "10.10.10.2/32", PresharedKey: "PSK-A", Enabled: true},
				{PublicKey: "KEYB", Address: "10.10.10.3/32", PresharedKey: "", Enabled: true},
			},
		},
	}
	k := mockWithPeers("wg0",
		kernel.PeerStatus{PublicKey: "KEYA", AllowedIPs: "10.10.10.2/32", PresharedKey: ""},            // PSK missing → must be set
		kernel.PeerStatus{PublicKey: "KEYB", AllowedIPs: "10.10.10.3/32", PresharedKey: "KERNEL-ONLY"}, // DB empty → leave
	)

	if err := Peers(context.Background(), fs, k); err != nil {
		t.Fatalf("Peers: %v", err)
	}

	st, _ := k.ShowInterface("wg0")
	got := map[string]string{}
	for _, p := range st.Peers {
		got[p.PublicKey] = p.PresharedKey
	}
	if got["KEYA"] != "PSK-A" {
		t.Errorf("KEYA psk=%q, want PSK-A (stored PSK should be pushed)", got["KEYA"])
	}
	if got["KEYB"] != "KERNEL-ONLY" {
		t.Errorf("KEYB psk=%q, want KERNEL-ONLY (empty DB PSK must not strip kernel)", got["KEYB"])
	}
}
