package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// newTestStore opens a fresh SQLite file under t.TempDir and applies migrations.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func insertInterface(t *testing.T, st *store.Store, name, subnet, addr string) model.Interface {
	t.Helper()
	ctx := context.Background()
	i := model.Interface{
		Name: name, Address: addr, Subnet: subnet,
		ListenPort: 51820, PrivateKey: "fake",
		PublicEndpoint: "example.com", PublicPort: 51820,
		DNS: "8.8.8.8", Keepalive: 25,
		Enabled: true, CreatedAt: time.Now().Unix(),
	}
	id, err := st.UpsertInterface(ctx, &i)
	if err != nil {
		t.Fatalf("upsert interface: %v", err)
	}
	i.ID = id
	return i
}

func TestNextFreeAddress(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	iface := insertInterface(t, st, "wg0", "10.8.1.0/24", "10.8.1.1/24")

	// First free must be .2 (.1 is the interface's own).
	addr, err := st.NextFreeAddress(ctx, iface)
	if err != nil {
		t.Fatalf("NextFreeAddress: %v", err)
	}
	if addr != "10.8.1.2/32" {
		t.Errorf("got %q, want 10.8.1.2/32", addr)
	}

	// Insert peers at .2 and .3; expect next to be .4.
	for _, a := range []string{"10.8.1.2/32", "10.8.1.3/32"} {
		if _, err := st.InsertPeer(ctx, &model.Peer{
			InterfaceID: iface.ID, Name: "p", PublicKey: a, PrivateKey: "x",
			Address: a, Enabled: true, Tags: "[]", CreatedAt: 0,
		}); err != nil {
			t.Fatalf("InsertPeer %s: %v", a, err)
		}
	}
	addr, err = st.NextFreeAddress(ctx, iface)
	if err != nil {
		t.Fatalf("NextFreeAddress after inserts: %v", err)
	}
	if addr != "10.8.1.4/32" {
		t.Errorf("got %q, want 10.8.1.4/32", addr)
	}
}

func TestNextFreeAddressSkipsBroadcast(t *testing.T) {
	// /30 has .0 (net), .1, .2, .3 (broadcast). .1 is the interface's own.
	// Only .2 should be allocatable.
	st := newTestStore(t)
	ctx := context.Background()
	iface := insertInterface(t, st, "wgtiny", "10.0.0.0/30", "10.0.0.1/30")

	addr, err := st.NextFreeAddress(ctx, iface)
	if err != nil {
		t.Fatalf("NextFreeAddress: %v", err)
	}
	if addr != "10.0.0.2/32" {
		t.Errorf("got %q, want 10.0.0.2/32", addr)
	}

	// Take .2; next allocation should fail (only broadcast left).
	if _, err := st.InsertPeer(ctx, &model.Peer{
		InterfaceID: iface.ID, Name: "p", PublicKey: "pk",
		PrivateKey: "x", Address: "10.0.0.2/32",
		Enabled: true, Tags: "[]", CreatedAt: 0,
	}); err != nil {
		t.Fatalf("InsertPeer: %v", err)
	}
	if _, err := st.NextFreeAddress(ctx, iface); err == nil {
		t.Fatal("expected error when subnet exhausted")
	}
}

func TestPeerCRUDRoundtrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	iface := insertInterface(t, st, "wg0", "10.8.1.0/24", "10.8.1.1/24")

	id, err := st.InsertPeer(ctx, &model.Peer{
		InterfaceID: iface.ID, Name: "alice",
		PublicKey: "PUB", PrivateKey: "PRIV",
		Address: "10.8.1.5/32", Enabled: true, Tags: "[]",
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := st.GetPeer(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "alice" || got.Address != "10.8.1.5/32" {
		t.Errorf("unexpected peer: %+v", got)
	}

	got2, err := st.GetPeerByPublicKey(ctx, "PUB")
	if err != nil {
		t.Fatalf("get by pubkey: %v", err)
	}
	if got2.ID != id {
		t.Errorf("pubkey lookup returned id %d, want %d", got2.ID, id)
	}

	newName := "alice2"
	if err := st.UpdatePeer(ctx, id, store.PeerPatch{Name: &newName}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.GetPeer(ctx, id)
	if got.Name != "alice2" {
		t.Errorf("rename failed, got %q", got.Name)
	}

	if err := st.DeletePeer(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetPeer(ctx, id); err == nil {
		t.Fatal("expected ErrNotFound after delete")
	}
}
