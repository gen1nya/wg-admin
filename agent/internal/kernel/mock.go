package kernel

import (
	"fmt"
	"sync"
	"time"
)

// Mock is an in-memory Kernel used in dev/tests. Seed it with realistic
// state to exercise the agent API without touching the host.
type Mock struct {
	mu         sync.Mutex
	Interfaces map[string]*InterfaceStatus
	IPSets     map[string][]string
	// Routes[table] = list of routes in that table
	Routes map[string][]RouteEntry
	Rules  []RuleEntry
	// NFT[tableName] = raw ruleset. Empty/missing key = table doesn't exist.
	NFT map[string]string
}

func NewMock() *Mock {
	m := &Mock{
		Interfaces: map[string]*InterfaceStatus{},
		IPSets:     map[string][]string{},
		Routes:     map[string][]RouteEntry{},
		NFT:        map[string]string{},
	}
	m.seedDefaults()
	return m
}

func (m *Mock) seedDefaults() {
	now := time.Now().Unix()
	m.Interfaces["wg0"] = &InterfaceStatus{
		Name:       "wg0",
		PublicKey:  "MOCK_WG0_PUBKEY_00000000000000000000000000=",
		ListenPort: 51820,
		Peers: []PeerStatus{
			{
				PublicKey:       "MOCK_PEER_A_000000000000000000000000000000=",
				Endpoint:        "203.0.113.10:51820",
				AllowedIPs:      "10.8.1.2/32",
				LatestHandshake: now - 30,
				RxBytes:         123456,
				TxBytes:         7890,
			},
		},
	}
	m.Interfaces["wg-clients-b"] = &InterfaceStatus{
		Name:       "wg-clients-b",
		PublicKey:  "MOCK_WGKZ_PUBKEY_0000000000000000000000000=",
		ListenPort: 51821,
		Peers:      nil,
	}
	m.IPSets["direct"] = []string{"77.88.0.0/16", "87.250.0.0/16"}
	m.IPSets["telegram-dc"] = []string{"91.108.0.0/16", "149.154.160.0/20"}
}

func (m *Mock) ListInterfaces() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.Interfaces))
	for n := range m.Interfaces {
		names = append(names, n)
	}
	return names, nil
}

func (m *Mock) ShowInterface(name string) (InterfaceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	iface, ok := m.Interfaces[name]
	if !ok {
		return InterfaceStatus{}, fmt.Errorf("%w: %s", ErrInterfaceNotFound, name)
	}
	return *iface, nil
}

func (m *Mock) SetPeer(iface, publicKey, allowedIPs, presharedKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ifc, ok := m.Interfaces[iface]
	if !ok {
		return fmt.Errorf("%w: %s", ErrInterfaceNotFound, iface)
	}
	for i, p := range ifc.Peers {
		if p.PublicKey == publicKey {
			ifc.Peers[i].AllowedIPs = allowedIPs
			if presharedKey != "" { // "" leaves the existing PSK untouched
				ifc.Peers[i].PresharedKey = presharedKey
			}
			return nil
		}
	}
	ifc.Peers = append(ifc.Peers, PeerStatus{
		PublicKey:    publicKey,
		AllowedIPs:   allowedIPs,
		PresharedKey: presharedKey,
	})
	return nil
}

func (m *Mock) RemovePeer(iface, publicKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ifc, ok := m.Interfaces[iface]
	if !ok {
		return fmt.Errorf("%w: %s", ErrInterfaceNotFound, iface)
	}
	out := ifc.Peers[:0]
	for _, p := range ifc.Peers {
		if p.PublicKey != publicKey {
			out = append(out, p)
		}
	}
	ifc.Peers = out
	return nil
}

func (m *Mock) IPSetList(name string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, ok := m.IPSets[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrIPSetNotFound, name)
	}
	out := make([]string, len(entries))
	copy(out, entries)
	return out, nil
}

func (m *Mock) IPSetDestroy(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.IPSets, name)
	return nil
}

func (m *Mock) IPSetReplace(name string, entries []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(entries))
	copy(cp, entries)
	m.IPSets[name] = cp
	return nil
}

func (m *Mock) RouteList(table string) ([]RouteEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs := m.Routes[table]
	out := make([]RouteEntry, len(rs))
	copy(out, rs)
	return out, nil
}

func (m *Mock) RouteReplace(r RouteEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs := m.Routes[r.Table]
	for i, ex := range rs {
		if ex.Dest == r.Dest {
			rs[i] = r
			m.Routes[r.Table] = rs
			return nil
		}
	}
	m.Routes[r.Table] = append(rs, r)
	return nil
}

func (m *Mock) RouteDelete(table, dest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rs := m.Routes[table]
	out := rs[:0]
	for _, r := range rs {
		if r.Dest != dest {
			out = append(out, r)
		}
	}
	m.Routes[table] = out
	return nil
}

func (m *Mock) RuleList() ([]RuleEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RuleEntry, len(m.Rules))
	copy(out, m.Rules)
	return out, nil
}

func (m *Mock) RuleAdd(r RuleEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.Rules {
		if ex.Priority == r.Priority {
			return fmt.Errorf("rule with priority %d already exists", r.Priority)
		}
	}
	m.Rules = append(m.Rules, r)
	return nil
}

func (m *Mock) RuleDel(priority int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.Rules[:0]
	for _, r := range m.Rules {
		if r.Priority != priority {
			out = append(out, r)
		}
	}
	m.Rules = out
	return nil
}

func (m *Mock) NFTList(table string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.NFT[table], nil
}

func (m *Mock) NFTApply(ruleset string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Crude mock: store the full ruleset as-is under a synthetic key.
	// Tests that care about structure can parse the stored string.
	m.NFT["_last_applied"] = ruleset
	return nil
}

func (m *Mock) Version() string { return "mock" }
