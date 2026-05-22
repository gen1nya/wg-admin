// Package devseed populates the DB with plausible records for dev and tests.
// Not used in production; real data comes from wg-agent import.
package devseed

import (
	"context"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/store"
	"github.com/gen1nya/wg-admin/agent/internal/wgkey"
)

// Seed writes mock interfaces, marks and exits into the store.
// Idempotent: skips if interfaces already exist.
func Seed(ctx context.Context, st *store.Store) error {
	existing, err := st.ListInterfaces(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}

	directMarkID, err := st.UpsertMark(ctx, &model.Mark{
		Fwmark: 0x0, Name: "direct", RoutingTable: "main",
		Description: "no mark; default routing",
	})
	if err != nil {
		return err
	}
	tunnelMarkID, err := st.UpsertMark(ctx, &model.Mark{
		Fwmark: 0x1, Name: "tunnel", RoutingTable: "wg_tunnel",
		Description: "fwmark 0x1 → route via wg_tunnel table",
	})
	if err != nil {
		return err
	}

	if _, err := st.UpsertExit(ctx, &model.Exit{
		Name: "direct", Kind: "direct", OutInterface: "ens3",
		MarkID: directMarkID, Masquerade: true, Enabled: true,
		Description: "Direct internet via server's public IP",
	}); err != nil {
		return err
	}
	if _, err := st.UpsertExit(ctx, &model.Exit{
		Name: "exit-a", Kind: "wg", OutInterface: "wg-exit-a",
		MarkID: tunnelMarkID, Enabled: true,
		Description: "Primary tunneled exit via wg-exit-a",
	}); err != nil {
		return err
	}
	if _, err := st.UpsertExit(ctx, &model.Exit{
		Name: "exit-b", Kind: "wg", OutInterface: "wg-exit-b",
		MarkID: tunnelMarkID, Enabled: true,
		Description: "Secondary tunneled exit via wg-exit-b",
	}); err != nil {
		return err
	}

	now := time.Now().Unix()

	// Generate real keypairs so /peers/{id}/config can derive pubkeys.
	wg0Priv, _, err := wgkey.GenPair()
	if err != nil {
		return err
	}
	wgBPriv, _, err := wgkey.GenPair()
	if err != nil {
		return err
	}

	// RFC1918-excluding allowed-ips, same shape the legacy wg-admin used.
	clientAllowed := "0.0.0.0/2, 64.0.0.0/3, 96.0.0.0/4, 112.0.0.0/5, 120.0.0.0/6, 124.0.0.0/7, 126.0.0.0/8, 128.0.0.0/2, 192.0.0.0/9, 192.128.0.0/11, 192.160.0.0/13, 192.169.0.0/16, 192.170.0.0/15, 192.172.0.0/14, 192.176.0.0/12, 192.192.0.0/10, 193.0.0.0/8, 194.0.0.0/7, 196.0.0.0/6, 200.0.0.0/5, 208.0.0.0/4, 224.0.0.0/3"

	ifaces := []model.Interface{
		{
			Name: "wg0", Address: "10.8.1.1/24", Subnet: "10.8.1.0/24",
			ListenPort: 51820, PrivateKey: wg0Priv,
			PublicEndpoint: "mock.example.com", PublicPort: 51820,
			DNS: "8.8.8.8, 1.1.1.1", Keepalive: 25,
			ClientAllowedIPs: clientAllowed,
			Enabled:          true, CreatedAt: now,
		},
		{
			Name: "wg-clients-b", Address: "10.8.3.1/24", Subnet: "10.8.3.0/24",
			ListenPort: 51821, PrivateKey: wgBPriv,
			PublicEndpoint: "mock.example.com", PublicPort: 51821,
			DNS: "8.8.8.8", Keepalive: 25,
			ClientAllowedIPs: "0.0.0.0/0",
			Enabled:          true, CreatedAt: now,
		},
	}
	for _, i := range ifaces {
		if _, err := st.UpsertInterface(ctx, &i); err != nil {
			return err
		}
	}
	return nil
}
