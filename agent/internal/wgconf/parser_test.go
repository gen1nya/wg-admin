package wgconf

import (
	"strings"
	"testing"
)

func TestParseServerConf(t *testing.T) {
	in := `
[Interface]
Address = 10.8.1.1/24
ListenPort = 51820
PrivateKey = gOwxzWIQQswpN9NdLRLfZ7V3fqWiyI1fuYW4bfSjlGc=
Table = off

PostUp = ip route add default dev wg-exit-a table wg_tunnel
PostDown = iptables -D FORWARD -i wg0 -o wg-exit-a -j ACCEPT 2>/dev/null || true

# alice
[Peer]
PublicKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
AllowedIPs = 10.8.1.2/32

# Test client 1
# testpeer
# stale-rename
[Peer]
PublicKey = BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=
AllowedIPs = 10.8.1.3/32
PersistentKeepalive = 25
`
	cfg, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Address != "10.8.1.1/24" {
		t.Errorf("Address=%q", cfg.Address)
	}
	if cfg.ListenPort != 51820 {
		t.Errorf("ListenPort=%d", cfg.ListenPort)
	}
	if cfg.PrivateKey == "" {
		t.Error("PrivateKey missing")
	}
	if len(cfg.PostUp) != 1 || len(cfg.PostDown) != 1 {
		t.Errorf("PostUp/PostDown: %d/%d", len(cfg.PostUp), len(cfg.PostDown))
	}
	if len(cfg.Peers) != 2 {
		t.Fatalf("peers=%d, want 2", len(cfg.Peers))
	}
	if got := cfg.Peers[0].Names; len(got) != 1 || got[0] != "alice" {
		t.Errorf("peer[0] names=%v, want [alice]", got)
	}
	if got := cfg.Peers[1].Names; len(got) != 3 || got[2] != "stale-rename" {
		t.Errorf("peer[1] names=%v, want 3 names ending with stale-rename", got)
	}
	if cfg.Peers[1].Keepalive != 25 {
		t.Errorf("keepalive=%d", cfg.Peers[1].Keepalive)
	}
}

func TestParseClientConf(t *testing.T) {
	in := `[Interface]
PrivateKey = client-priv==
Address = 10.8.1.5/32
DNS = 8.8.8.8, 1.1.1.1
MTU = 1340

[Peer]
PublicKey = server-pub==
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`
	cfg, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.PrivateKey != "client-priv==" {
		t.Errorf("PrivateKey=%q", cfg.PrivateKey)
	}
	if cfg.DNS != "8.8.8.8, 1.1.1.1" {
		t.Errorf("DNS=%q", cfg.DNS)
	}
	if cfg.MTU != 1340 {
		t.Errorf("MTU=%d", cfg.MTU)
	}
	if len(cfg.Peers) != 1 {
		t.Fatalf("peers=%d", len(cfg.Peers))
	}
	if cfg.Peers[0].Endpoint != "vpn.example.com:51820" {
		t.Errorf("endpoint=%q", cfg.Peers[0].Endpoint)
	}
}
