// Package kernel abstracts reads and writes to kernel-level state:
// WireGuard interfaces, iptables chains, ipsets, ip routes.
//
// Implementations:
//   - Mock: in-memory state, used in dev and tests
//   - Real: shells out to wg/ip/iptables/ipset (future netlink/wgctrl)
package kernel

type PeerStatus struct {
	PublicKey       string `json:"public_key"`
	Endpoint        string `json:"endpoint"`
	AllowedIPs      string `json:"allowed_ips"`
	LatestHandshake int64  `json:"latest_handshake"` // unix seconds
	RxBytes         int64  `json:"rx_bytes"`
	TxBytes         int64  `json:"tx_bytes"`
}

type InterfaceStatus struct {
	Name       string       `json:"name"`
	PublicKey  string       `json:"public_key"`
	ListenPort int          `json:"listen_port"`
	FwMark     int          `json:"fwmark"`
	Peers      []PeerStatus `json:"peers"`
}

// RouteEntry is one row in a routing table. Dest is either "default" or a
// CIDR. Dev and Via are optional (via is the nexthop gateway).
type RouteEntry struct {
	Table string `json:"table"`
	Dest  string `json:"dest"`
	Dev   string `json:"dev,omitempty"`
	Via   string `json:"via,omitempty"`
}

// RuleEntry is one `ip rule` row we care about: fwmark → lookup table.
// Other rule forms (from/to/iif/oif/suppress) aren't modelled — agent only
// owns the fwmark→table pattern.
type RuleEntry struct {
	Priority int    `json:"priority"`
	Fwmark   uint32 `json:"fwmark"`
	Table    string `json:"table"`
}

// Kernel is the narrow surface the agent uses to inspect and mutate host state.
// Every method either returns current state or applies a single discrete change.
type Kernel interface {
	// WireGuard — read
	ListInterfaces() ([]string, error)
	ShowInterface(name string) (InterfaceStatus, error)

	// WireGuard — peer ops (Tier-1, immediate)
	SetPeer(iface, publicKey, allowedIPs string) error
	RemovePeer(iface, publicKey string) error

	// ipset
	IPSetList(name string) ([]string, error)
	IPSetReplace(name string, entries []string) error
	IPSetDestroy(name string) error // idempotent: missing set is not an error

	// Routing tables — agent touches only tables in its allowlist (caller-enforced)
	RouteList(table string) ([]RouteEntry, error)
	RouteReplace(r RouteEntry) error
	RouteDelete(table, dest string) error // idempotent: missing route is not an error

	// ip rule — fwmark→table pattern only. Filter by priority range, caller-enforced.
	RuleList() ([]RuleEntry, error)
	RuleAdd(r RuleEntry) error
	RuleDel(priority int) error // idempotent

	// nftables — agent owns exactly one table: `inet <name>`, default "wg-admin"
	NFTList(table string) (string, error) // `nft list table inet <table>` raw output; empty if table missing
	NFTApply(ruleset string) error         // `nft -f -` atomic

	// Informational
	Version() string
}
