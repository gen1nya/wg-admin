// Package renderer turns DB model records into concrete artifacts:
// wg-quick style client .conf, nft rulesets, ipset restore blobs, etc.
//
// For MVP only ClientConfig is implemented. Server-side artifacts
// (nft / iptables / ipset) will land with the plan/apply tier.
package renderer

import (
	"fmt"
	"strings"

	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/wgkey"
)

// ClientConfig renders the wg-quick style .conf for a peer.
// The server's public key is derived from the interface's private key.
func ClientConfig(iface model.Interface, peer model.Peer) (string, error) {
	serverPub, err := wgkey.PublicFromPrivate(iface.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("derive server pubkey: %w", err)
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", peer.PrivateKey)
	fmt.Fprintf(&b, "Address = %s\n", peer.Address)
	if iface.DNS != "" {
		fmt.Fprintf(&b, "DNS = %s\n", iface.DNS)
	}
	if iface.MTU != nil {
		fmt.Fprintf(&b, "MTU = %d\n", *iface.MTU)
	}

	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", serverPub)
	fmt.Fprintf(&b, "Endpoint = %s:%d\n", iface.PublicEndpoint, iface.PublicPort)
	allowed := iface.ClientAllowedIPs
	if allowed == "" {
		allowed = "0.0.0.0/0"
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", allowed)
	if iface.Keepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", iface.Keepalive)
	}
	return b.String(), nil
}
