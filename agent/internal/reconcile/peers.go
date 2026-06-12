// Package reconcile heals kernel state after a reboot by aligning it with
// the DB. It's invoked once at agent startup; the API surface is plain
// functions taking the store and kernel, so tests can swap in mocks.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// Store is the subset of *store.Store that peer reconcile needs.
// Defining it as an interface lets tests pass a fake without touching sqlite.
type Store interface {
	ListInterfaces(ctx context.Context) ([]model.Interface, error)
	ListPeersByInterface(ctx context.Context, ifaceID int64) ([]model.Peer, error)
	LogAudit(ctx context.Context, actor, action, entityType string, entityID *int64, payload string) error
}

// InterfaceWaitTimeout bounds how long we wait for wg-quick to bring an
// interface up before giving up and moving on. Agent must not hang boot.
const InterfaceWaitTimeout = 10 * time.Second

// Peers aligns kernel peers with DB peers for every enabled clients-role
// interface. Mesh-role interfaces are skipped on purpose: their peers have
// AllowedIPs wider than DB `address` (e.g. 0.0.0.0/0 for crypto-routing),
// and DB doesn't carry that, so a naive sync would lobotomise the tunnel.
//
// Reconcile is best-effort: per-interface errors are logged and we move on.
// Returns the first error encountered or nil.
func Peers(ctx context.Context, st Store, k kernel.Kernel) error {
	ifaces, err := st.ListInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("list interfaces: %w", err)
	}

	var firstErr error
	for _, intf := range ifaces {
		if !intf.Enabled {
			continue
		}
		if intf.Role != model.RoleClients {
			slog.Debug("peer reconcile: skipping non-clients interface", "name", intf.Name, "role", intf.Role)
			continue
		}

		if err := waitForInterface(ctx, k, intf.Name, InterfaceWaitTimeout); err != nil {
			slog.Warn("peer reconcile: interface not up, skipping", "name", intf.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		desired, err := st.ListPeersByInterface(ctx, intf.ID)
		if err != nil {
			slog.Error("peer reconcile: load peers", "iface", intf.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		result, err := syncPeers(k, intf.Name, desired)
		if err != nil {
			slog.Error("peer reconcile: sync", "iface", intf.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			// keep going to audit
		}
		slog.Info("peer reconcile",
			"iface", intf.Name,
			"added", result.Added,
			"removed", result.Removed,
			"updated", result.Updated,
			"unchanged", result.Unchanged,
		)
		id := intf.ID
		payload := fmt.Sprintf(`{"iface":%q,"added":%d,"removed":%d,"updated":%d,"unchanged":%d}`,
			intf.Name, result.Added, result.Removed, result.Updated, result.Unchanged)
		_ = st.LogAudit(ctx, "boot-reconcile", "boot.reconcile.peers", "interface", &id, payload)
	}
	return firstErr
}

// SyncResult counts what happened during one interface sync — useful for
// audit and tests.
type SyncResult struct {
	Added     int
	Removed   int
	Updated   int
	Unchanged int
}

// syncPeers aligns one interface's kernel peers with the given desired set.
// DB is the source of truth: peers missing from desired are removed, peers
// missing from kernel are added, peers whose allowed-ips drifted are fixed.
func syncPeers(k kernel.Kernel, iface string, desired []model.Peer) (SyncResult, error) {
	var res SyncResult

	st, err := k.ShowInterface(iface)
	if err != nil {
		return res, fmt.Errorf("show %s: %w", iface, err)
	}
	type peerState struct{ allowedIPs, psk string }
	current := make(map[string]peerState, len(st.Peers))
	for _, p := range st.Peers {
		current[p.PublicKey] = peerState{p.AllowedIPs, p.PresharedKey}
	}

	want := make(map[string]model.Peer, len(desired))
	for _, p := range desired {
		if !p.Enabled {
			continue
		}
		want[p.PublicKey] = p
	}

	// Remove peers that aren't in DB. Agent is authoritative for clients iface.
	for pk := range current {
		if _, ok := want[pk]; ok {
			continue
		}
		if err := k.RemovePeer(iface, pk); err != nil {
			slog.Warn("remove peer", "iface", iface, "pubkey", shortKey(pk), "err", err)
			continue
		}
		res.Removed++
	}
	// Upsert: add new peers, realign drifted allowed-ips, and set a stored PSK
	// when it differs. A stored PSK of "" is treated as "don't touch" so we
	// never strip a kernel PSK that the DB simply doesn't know about yet.
	for pk, p := range want {
		cur, had := current[pk]
		addrOK := had && cur.allowedIPs == p.Address
		pskOK := p.PresharedKey == "" || (had && cur.psk == p.PresharedKey)
		if addrOK && pskOK {
			res.Unchanged++
			continue
		}
		if err := k.SetPeer(iface, pk, p.Address, p.PresharedKey); err != nil {
			slog.Warn("set peer", "iface", iface, "pubkey", shortKey(pk), "err", err)
			continue
		}
		if had {
			res.Updated++
		} else {
			res.Added++
		}
	}
	return res, nil
}

// waitForInterface polls until ShowInterface succeeds or timeout expires.
// wg-quick@<iface>.service starts in parallel with wg-agent; we must not
// race past it.
func waitForInterface(ctx context.Context, k kernel.Kernel, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		_, err := k.ShowInterface(name)
		if err == nil {
			return nil
		}
		if !errors.Is(err, kernel.ErrInterfaceNotFound) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("interface %s did not appear within %s", name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func shortKey(pk string) string {
	if len(pk) <= 8 {
		return pk
	}
	return pk[:8] + "…"
}

// Compile-time check that *store.Store satisfies our narrower Store
// interface. Keeps main.go wiring simple.
var _ Store = (*store.Store)(nil)
