// Package importer ingests existing /etc/wireguard-style directories into
// the agent's SQLite. Does not touch the kernel: we write desired state to
// the DB, reconciliation happens later via the plan/apply flow.
package importer

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/store"
	"github.com/gen1nya/wg-admin/agent/internal/wgconf"
	"github.com/gen1nya/wg-admin/agent/internal/wgkey"
)

type Options struct {
	FromDir      string   // /etc/wireguard-like root
	Only         []string // restrict to these interface names (empty = all)
	DryRun       bool
	PublicHost   string // applied to every interface if set (else left empty)
}

type Stats struct {
	Interfaces  int
	Peers       int
	PeersNoKey  int // peers imported without a stored client private key
	Skipped     []string
}

type clientInfo struct {
	PrivateKey string
	PublicKey  string
	Address    string
	Endpoint   string
	DNS        string
	MTU        int
	AllowedIPs string
	Name       string
	SourcePath string
}

// Run imports interfaces from Options.FromDir. The path can be either:
//   - a directory with *.conf files (optional sibling `clients*/` dirs with
//     client confs used to harvest private keys + names)
//   - a single *.conf file (no client sidecar lookup)
//
// Writes results into the store; Stats summarises what happened.
func Run(ctx context.Context, st *store.Store, opt Options) (*Stats, error) {
	if opt.FromDir == "" {
		return nil, fmt.Errorf("FromDir is required")
	}
	info, err := os.Stat(opt.FromDir)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", opt.FromDir, err)
	}

	var (
		clients    map[string]*clientInfo
		confPaths  []string
	)
	if info.IsDir() {
		clients, err = scanClientsDirs(opt.FromDir)
		if err != nil {
			return nil, fmt.Errorf("scan clients: %w", err)
		}
		entries, err := os.ReadDir(opt.FromDir)
		if err != nil {
			return nil, err
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".conf") {
				continue
			}
			confPaths = append(confPaths, filepath.Join(opt.FromDir, ent.Name()))
		}
	} else {
		if !strings.HasSuffix(opt.FromDir, ".conf") {
			return nil, fmt.Errorf("%q is not a .conf file", opt.FromDir)
		}
		clients = map[string]*clientInfo{}
		confPaths = []string{opt.FromDir}
	}

	onlySet := map[string]bool{}
	for _, n := range opt.Only {
		onlySet[n] = true
	}

	stats := &Stats{}
	now := time.Now().Unix()

	for _, confPath := range confPaths {
		ifaceName := strings.TrimSuffix(filepath.Base(confPath), ".conf")
		if len(onlySet) > 0 && !onlySet[ifaceName] {
			stats.Skipped = append(stats.Skipped, ifaceName)
			continue
		}
		cfg, err := wgconf.ParseFile(confPath)
		if err != nil {
			slog.Warn("parse failed, skipping", "conf", confPath, "err", err)
			stats.Skipped = append(stats.Skipped, ifaceName+"(parse error)")
			continue
		}
		// Only treat as server conf when [Interface] has Address.
		// Client confs without server role would still parse, but our
		// import layout is: server confs live in FromDir, client confs
		// live in FromDir/clients*.
		if cfg.Address == "" {
			stats.Skipped = append(stats.Skipped, ifaceName+"(no Address)")
			continue
		}

		// Pick one of this interface's clients to harvest
		// endpoint/DNS/ClientAllowedIPs from.
		var sample *clientInfo
		for i := range cfg.Peers {
			if c, ok := clients[cfg.Peers[i].PublicKey]; ok {
				sample = c
				break
			}
		}

		subnet, err := subnetFromAddress(cfg.Address)
		if err != nil {
			slog.Warn("bad Address, skipping", "conf", confPath, "addr", cfg.Address, "err", err)
			stats.Skipped = append(stats.Skipped, ifaceName+"(bad Address)")
			continue
		}

		mtu := cfg.MTU
		var mtuPtr *int
		if mtu > 0 {
			mtuPtr = &mtu
		}

		publicHost := opt.PublicHost
		publicPort := cfg.ListenPort
		dns := ""
		clientAllowed := "0.0.0.0/0"
		if sample != nil {
			host, port := splitEndpoint(sample.Endpoint)
			if publicHost == "" {
				publicHost = host
			}
			if port != 0 {
				publicPort = port
			}
			dns = sample.DNS
			if sample.AllowedIPs != "" {
				clientAllowed = sample.AllowedIPs
			}
			if mtuPtr == nil && sample.MTU > 0 {
				m := sample.MTU
				mtuPtr = &m
			}
		}

		iface := model.Interface{
			Name:             ifaceName,
			Address:          cfg.Address,
			Subnet:           subnet,
			ListenPort:       cfg.ListenPort,
			MTU:              mtuPtr,
			PrivateKey:       cfg.PrivateKey,
			PublicEndpoint:   publicHost,
			PublicPort:       publicPort,
			DNS:              dns,
			Keepalive:        25,
			ClientAllowedIPs: clientAllowed,
			CustomPostUp:     strings.Join(cfg.PostUp, "\n"),
			CustomPostDown:   strings.Join(cfg.PostDown, "\n"),
			Role:             detectRole(cfg.Peers),
			Enabled:          true,
			CreatedAt:        now,
		}

		if opt.DryRun {
			slog.Info("would upsert interface", "name", ifaceName, "peers", len(cfg.Peers))
			stats.Interfaces++
			stats.Peers += len(cfg.Peers)
			continue
		}

		ifaceID, err := st.UpsertInterface(ctx, &iface)
		if err != nil {
			return stats, fmt.Errorf("upsert interface %s: %w", ifaceName, err)
		}
		iface.ID = ifaceID
		stats.Interfaces++

		// Re-fetch existing peers to avoid duplicate-key inserts on re-run.
		existing, err := st.ListPeersByInterface(ctx, ifaceID)
		if err != nil {
			return stats, fmt.Errorf("list peers for %s: %w", ifaceName, err)
		}
		havePub := map[string]bool{}
		for _, p := range existing {
			havePub[p.PublicKey] = true
		}

		for _, pPeer := range cfg.Peers {
			if pPeer.PublicKey == "" {
				continue
			}
			if havePub[pPeer.PublicKey] {
				continue
			}
			name := chooseName(pPeer.Names)
			privKey := ""
			address := firstAllowedIP(pPeer.AllowedIPs)
			if c, ok := clients[pPeer.PublicKey]; ok {
				privKey = c.PrivateKey
				if c.Name != "" {
					name = c.Name
				}
				if c.Address != "" {
					address = c.Address
				}
			}
			if address == "" {
				// no AllowedIPs on server side? skip
				stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s/%s(no address)", ifaceName, shortKey(pPeer.PublicKey)))
				continue
			}
			if privKey == "" {
				stats.PeersNoKey++
			}
			peer := &model.Peer{
				InterfaceID: ifaceID,
				Name:        name,
				PublicKey:   pPeer.PublicKey,
				PrivateKey:  privKey,
				Address:     address,
				Enabled:     true,
				Tags:        "[]",
				CreatedAt:   now,
			}
			if _, err := st.InsertPeer(ctx, peer); err != nil {
				return stats, fmt.Errorf("insert peer %s on %s: %w", shortKey(pPeer.PublicKey), ifaceName, err)
			}
			stats.Peers++
		}
	}
	return stats, nil
}

// scanClientsDirs walks any directory matching "clients*" under root and
// builds a map of pubkey → clientInfo by parsing *.conf files.
func scanClientsDirs(root string) (map[string]*clientInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := map[string]*clientInfo{}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "clients") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".conf") {
				continue
			}
			path := filepath.Join(dir, f.Name())
			c, err := parseClientConf(path)
			if err != nil {
				slog.Warn("client conf parse failed", "path", path, "err", err)
				continue
			}
			// Try to read companion .name file.
			namePath := strings.TrimSuffix(path, ".conf") + ".name"
			if b, err := os.ReadFile(namePath); err == nil {
				c.Name = strings.TrimSpace(string(b))
			}
			out[c.PublicKey] = c
		}
	}
	return out, nil
}

func parseClientConf(path string) (*clientInfo, error) {
	cfg, err := wgconf.ParseFile(path)
	if err != nil {
		return nil, err
	}
	if cfg.PrivateKey == "" || len(cfg.Peers) == 0 {
		return nil, fmt.Errorf("not a client conf (missing PrivateKey or Peer)")
	}
	pub, err := wgkey.PublicFromPrivate(cfg.PrivateKey)
	if err != nil {
		return nil, err
	}
	srvPeer := cfg.Peers[0]
	return &clientInfo{
		PrivateKey: cfg.PrivateKey,
		PublicKey:  pub,
		Address:    normalizeClientAddress(cfg.Address),
		Endpoint:   srvPeer.Endpoint,
		DNS:        cfg.DNS,
		MTU:        cfg.MTU,
		AllowedIPs: srvPeer.AllowedIPs,
		SourcePath: path,
	}, nil
}

// subnetFromAddress: "10.8.1.1/24" -> "10.8.1.0/24"
func subnetFromAddress(addr string) (string, error) {
	p, err := netip.ParsePrefix(addr)
	if err != nil {
		return "", err
	}
	return p.Masked().String(), nil
}

// normalizeClientAddress turns "10.8.1.5/24" into "10.8.1.5/32".
// WireGuard client confs use /24 (so LAN-ish routes work) but our Peer.Address
// models the individual host IP.
func normalizeClientAddress(addr string) string {
	if addr == "" {
		return ""
	}
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i] + "/32"
	}
	return addr + "/32"
}

// firstAllowedIP extracts the single host from "10.8.1.5/32" or "10.8.1.5/32,
// fd00::5/128" etc. Returns the IPv4 portion as-is (agent is IPv4-only for MVP).
func firstAllowedIP(s string) string {
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, ":") {
			continue // skip IPv6
		}
		return p
	}
	return ""
}

// splitEndpoint: "vpn.example.com:51820" → ("vpn.example.com", 51820).
func splitEndpoint(ep string) (string, int) {
	i := strings.LastIndexByte(ep, ':')
	if i < 0 {
		return ep, 0
	}
	host := ep[:i]
	port, _ := strconv.Atoi(ep[i+1:])
	return host, port
}

// chooseName picks the most trustworthy candidate from a pile of pre-[Peer]
// comments. Heuristic: take the last one — later edits usually win.
func chooseName(names []string) string {
	for i := len(names) - 1; i >= 0; i-- {
		n := strings.TrimSpace(names[i])
		if n != "" && !strings.EqualFold(n, "Test client 1") {
			return n
		}
	}
	return ""
}

// detectRole picks 'mesh' iff any peer has AllowedIPs that aren't a single
// /32 host route — i.e. a subnet, a default route, or IPv6. Typical client
// interfaces have peers with exactly one /32; mesh/exit tunnels carry
// 0.0.0.0/0 or a remote subnet on the single peer.
func detectRole(peers []wgconf.Peer) string {
	if len(peers) == 0 {
		return model.RoleClients
	}
	for _, p := range peers {
		if !isClientPeer(p.AllowedIPs) {
			return model.RoleMesh
		}
	}
	return model.RoleClients
}

// isClientPeer: AllowedIPs has exactly one IPv4 /32 entry (ignoring blanks).
func isClientPeer(allowedIPs string) bool {
	count := 0
	for _, p := range strings.Split(allowedIPs, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			return false
		}
		if !prefix.Addr().Is4() || prefix.Bits() != 32 {
			return false
		}
		count++
	}
	return count == 1
}

func shortKey(k string) string {
	if len(k) < 12 {
		return k
	}
	return k[:12] + "…"
}
