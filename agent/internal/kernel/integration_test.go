//go:build integration

// Integration tests for kernel.Real. Every test runs inside a fresh network
// namespace (`ip netns add`) and tears it down on Cleanup. The host's
// routing, firewall and WireGuard interfaces are never touched.
//
//   sudo go test -tags integration ./internal/kernel/...
//
// Requires root or passwordless sudo. Skips otherwise.
package kernel

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/wgkey"
)

func sudoPrefix() []string {
	if os.Geteuid() == 0 {
		return nil
	}
	return []string{"sudo", "-n"}
}

func skipIfNotRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		return
	}
	if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
		t.Skip("needs root or passwordless sudo")
	}
}

func runRoot(t *testing.T, args ...string) {
	t.Helper()
	argv := append(sudoPrefix(), args...)
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("root cmd %v: %v\n%s", argv, err, out)
	}
}

type testNS struct {
	name string
	t    *testing.T
}

func newNetns(t *testing.T) *testNS {
	t.Helper()
	name := fmt.Sprintf("wgadm-it-%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	runRoot(t, "ip", "netns", "add", name)
	ns := &testNS{name: name, t: t}
	t.Cleanup(func() {
		argv := append(sudoPrefix(), "ip", "netns", "del", name)
		_ = exec.Command(argv[0], argv[1:]...).Run()
	})
	return ns
}

// in executes a command inside the namespace.
func (n *testNS) in(args ...string) {
	n.t.Helper()
	argv := append(sudoPrefix(), "ip", "netns", "exec", n.name)
	argv = append(argv, args...)
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		n.t.Fatalf("netns %s %v: %v\n%s", n.name, args, err, out)
	}
}

// setupWG creates a wireguard interface inside the namespace with the given
// private key and listen port, brings it up.
func (n *testNS) setupWG(ifname, priv string, port int) {
	n.t.Helper()
	privFile := filepath.Join(n.t.TempDir(), "priv-"+ifname)
	if err := os.WriteFile(privFile, []byte(priv+"\n"), 0o600); err != nil {
		n.t.Fatalf("write priv: %v", err)
	}
	n.in("ip", "link", "add", ifname, "type", "wireguard")
	n.in("wg", "set", ifname, "private-key", privFile, "listen-port", fmt.Sprintf("%d", port))
	n.in("ip", "link", "set", ifname, "up")
}

func newTestReal(ns *testNS) *Real {
	return &Real{
		WgPath:     "wg",
		IPPath:     "ip",
		IPSetPath:  "ipset",
		NFTPath:    "nft",
		Timeout:    10 * time.Second,
		Netns:      ns.name,
		SudoPrefix: sudoPrefix(),
	}
}

// checkWireGuardAvailable skips the test if the wireguard module can't be
// loaded (unlikely on modern kernels, but sanity-check).
func checkWireGuardAvailable(t *testing.T) {
	t.Helper()
	argv := append(sudoPrefix(), "modprobe", "wireguard")
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Skipf("wireguard module unavailable: %v\n%s", err, out)
	}
}

func TestRealListAndShowInterface(t *testing.T) {
	skipIfNotRoot(t)
	checkWireGuardAvailable(t)
	ns := newNetns(t)

	priv, pub, err := wgkey.GenPair()
	if err != nil {
		t.Fatalf("genpair: %v", err)
	}
	ns.setupWG("wg-it-a", priv, 51870)

	r := newTestReal(ns)

	ifaces, err := r.ListInterfaces()
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(ifaces) != 1 || ifaces[0] != "wg-it-a" {
		t.Errorf("ListInterfaces=%v, want [wg-it-a]", ifaces)
	}

	st, err := r.ShowInterface("wg-it-a")
	if err != nil {
		t.Fatalf("ShowInterface: %v", err)
	}
	if st.Name != "wg-it-a" {
		t.Errorf("name=%q", st.Name)
	}
	if st.ListenPort != 51870 {
		t.Errorf("listen_port=%d, want 51870", st.ListenPort)
	}
	if st.PublicKey != pub {
		t.Errorf("public_key mismatch:\n got  %q\n want %q", st.PublicKey, pub)
	}
	if len(st.Peers) != 0 {
		t.Errorf("peers=%d, want 0", len(st.Peers))
	}
}

func TestRealSetRemovePeer(t *testing.T) {
	skipIfNotRoot(t)
	checkWireGuardAvailable(t)
	ns := newNetns(t)

	priv, _, _ := wgkey.GenPair()
	ns.setupWG("wg-it-b", priv, 51871)

	r := newTestReal(ns)

	_, peerPub, _ := wgkey.GenPair()

	if err := r.SetPeer("wg-it-b", peerPub, "10.99.99.2/32"); err != nil {
		t.Fatalf("SetPeer: %v", err)
	}

	st, err := r.ShowInterface("wg-it-b")
	if err != nil {
		t.Fatalf("ShowInterface after SetPeer: %v", err)
	}
	if len(st.Peers) != 1 {
		t.Fatalf("peers=%d, want 1", len(st.Peers))
	}
	if st.Peers[0].PublicKey != peerPub {
		t.Errorf("peer pubkey mismatch:\n got  %q\n want %q", st.Peers[0].PublicKey, peerPub)
	}
	if st.Peers[0].AllowedIPs != "10.99.99.2/32" {
		t.Errorf("allowed-ips=%q", st.Peers[0].AllowedIPs)
	}

	// Upsert: same pubkey, different allowed-ips — peer count stays 1.
	if err := r.SetPeer("wg-it-b", peerPub, "10.99.99.2/32,10.99.99.3/32"); err != nil {
		t.Fatalf("SetPeer upsert: %v", err)
	}
	st, _ = r.ShowInterface("wg-it-b")
	if len(st.Peers) != 1 {
		t.Errorf("peers after upsert=%d, want 1", len(st.Peers))
	}
	if !strings.Contains(st.Peers[0].AllowedIPs, "10.99.99.3/32") {
		t.Errorf("upsert did not add new IP: %q", st.Peers[0].AllowedIPs)
	}

	// Remove.
	if err := r.RemovePeer("wg-it-b", peerPub); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	st, _ = r.ShowInterface("wg-it-b")
	if len(st.Peers) != 0 {
		t.Errorf("peers after remove=%d, want 0", len(st.Peers))
	}

	// Remove again — idempotent, no error.
	if err := r.RemovePeer("wg-it-b", peerPub); err != nil {
		t.Errorf("RemovePeer idempotent: %v", err)
	}
}

func TestRealMissingInterface(t *testing.T) {
	skipIfNotRoot(t)
	ns := newNetns(t)

	r := newTestReal(ns)

	_, err := r.ShowInterface("wg-missing")
	if !errors.Is(err, ErrInterfaceNotFound) {
		t.Errorf("ShowInterface missing: %v, want ErrInterfaceNotFound", err)
	}

	// Valid pubkey but missing interface.
	pub := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	err = r.SetPeer("wg-missing", pub, "10.0.0.2/32")
	if !errors.Is(err, ErrInterfaceNotFound) {
		t.Errorf("SetPeer on missing iface: %v, want ErrInterfaceNotFound", err)
	}
}

func TestRealListInterfacesEmpty(t *testing.T) {
	skipIfNotRoot(t)
	ns := newNetns(t)
	r := newTestReal(ns)
	ifaces, err := r.ListInterfaces()
	if err != nil {
		t.Fatalf("ListInterfaces: %v", err)
	}
	if len(ifaces) != 0 {
		t.Errorf("fresh netns had interfaces: %v", ifaces)
	}
}

// --- Phase 2/3 kernel ops inside netns ---

// setupLoopback brings up lo so routes requiring a device have one.
func (n *testNS) setupLoopback() {
	n.in("ip", "link", "set", "lo", "up")
}

func TestRealRouteOps(t *testing.T) {
	skipIfNotRoot(t)
	ns := newNetns(t)
	ns.setupLoopback()
	r := newTestReal(ns)

	// Fresh namespace: empty table 999.
	routes, err := r.RouteList("999")
	if err != nil {
		t.Fatalf("RouteList empty: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("fresh table 999 had %d routes", len(routes))
	}

	// Add a route to a dummy netns interface (lo).
	rt := RouteEntry{Table: "999", Dest: "10.77.0.0/24", Dev: "lo"}
	if err := r.RouteReplace(rt); err != nil {
		t.Fatalf("RouteReplace: %v", err)
	}
	routes, _ = r.RouteList("999")
	if len(routes) != 1 || routes[0].Dest != "10.77.0.0/24" || routes[0].Dev != "lo" {
		t.Errorf("after replace: %+v", routes)
	}

	// Upsert (replace same dest, same dev — should not create duplicate).
	if err := r.RouteReplace(rt); err != nil {
		t.Fatalf("RouteReplace idempotent: %v", err)
	}
	routes, _ = r.RouteList("999")
	if len(routes) != 1 {
		t.Errorf("upsert created duplicate: %+v", routes)
	}

	// Delete, then delete-missing (idempotent).
	if err := r.RouteDelete("999", "10.77.0.0/24"); err != nil {
		t.Fatalf("RouteDelete: %v", err)
	}
	if err := r.RouteDelete("999", "10.77.0.0/24"); err != nil {
		t.Errorf("RouteDelete missing: want nil, got %v", err)
	}
	routes, _ = r.RouteList("999")
	if len(routes) != 0 {
		t.Errorf("after delete: %+v", routes)
	}
}

func TestRealRuleOps(t *testing.T) {
	skipIfNotRoot(t)
	ns := newNetns(t)
	r := newTestReal(ns)

	// Fresh ns has default rules (0, 32766, 32767) but none with fwmark.
	rules, err := r.RuleList()
	if err != nil {
		t.Fatalf("RuleList: %v", err)
	}
	for _, rr := range rules {
		if rr.Priority >= 10000 && rr.Priority < 20000 {
			t.Errorf("unexpected rule in agent range: %+v", rr)
		}
	}

	// Add a rule, list, del.
	rule := RuleEntry{Priority: 12345, Fwmark: 0x1, Table: "999"}
	if err := r.RuleAdd(rule); err != nil {
		t.Fatalf("RuleAdd: %v", err)
	}
	rules, _ = r.RuleList()
	found := false
	for _, rr := range rules {
		if rr.Priority == 12345 {
			found = true
			if rr.Fwmark != 1 || rr.Table != "999" {
				t.Errorf("round-trip lost data: %+v", rr)
			}
		}
	}
	if !found {
		t.Errorf("added rule not listed: %+v", rules)
	}

	if err := r.RuleDel(12345); err != nil {
		t.Fatalf("RuleDel: %v", err)
	}
	if err := r.RuleDel(12345); err != nil {
		t.Errorf("RuleDel idempotent: got %v", err)
	}
}

// TestRealIPSetOps covers ipset round-trip on the real binary.
// ipset is NOT netns-scoped — tests run against host state. Each test uses
// a unique prefix "kit" + pid + time so parallel / prior runs don't collide,
// and t.Cleanup destroys any sets with that prefix.
func TestRealIPSetOps(t *testing.T) {
	skipIfNotRoot(t)
	if _, err := exec.LookPath("ipset"); err != nil {
		t.Skip("ipset binary unavailable")
	}

	prefix := fmt.Sprintf("kit%d_%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	setA := prefix + "_a"
	setB := prefix + "_b"
	t.Cleanup(func() {
		for _, name := range []string{setA, setA + "_new", setB, setB + "_new"} {
			argv := append(sudoPrefix(), "ipset", "destroy", name)
			_ = exec.Command(argv[0], argv[1:]...).Run()
		}
	})

	// Real uses host binaries directly (no netns wrap) since ipset is global.
	r := &Real{
		WgPath:     "wg",
		IPPath:     "ip",
		IPSetPath:  "ipset",
		NFTPath:    "nft",
		Timeout:    10 * time.Second,
		SudoPrefix: sudoPrefix(),
	}

	// Missing set: List → ErrIPSetNotFound
	if _, err := r.IPSetList(setA); !errors.Is(err, ErrIPSetNotFound) {
		t.Errorf("list missing: got %v, want ErrIPSetNotFound", err)
	}

	// Replace creates the set.
	if err := r.IPSetReplace(setA, []string{"10.11.0.0/16", "10.12.0.0/16"}); err != nil {
		t.Fatalf("IPSetReplace create: %v", err)
	}
	entries, err := r.IPSetList(setA)
	if err != nil {
		t.Fatalf("IPSetList: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("entries=%v", entries)
	}

	// Replace with different content (atomic swap).
	if err := r.IPSetReplace(setA, []string{"10.11.0.0/16", "10.13.0.0/16"}); err != nil {
		t.Fatalf("IPSetReplace swap: %v", err)
	}
	entries, _ = r.IPSetList(setA)
	got := map[string]bool{}
	for _, e := range entries {
		got[e] = true
	}
	if !got["10.13.0.0/16"] || got["10.12.0.0/16"] {
		t.Errorf("atomic swap left wrong content: %v", entries)
	}

	// Destroy is idempotent.
	if err := r.IPSetDestroy(setA); err != nil {
		t.Fatalf("IPSetDestroy: %v", err)
	}
	if err := r.IPSetDestroy(setA); err != nil {
		t.Errorf("IPSetDestroy idempotent: %v", err)
	}
	if _, err := r.IPSetList(setA); !errors.Is(err, ErrIPSetNotFound) {
		t.Errorf("after destroy: %v, want ErrIPSetNotFound", err)
	}
}

func TestRealNFTOps(t *testing.T) {
	skipIfNotRoot(t)
	ns := newNetns(t)
	r := newTestReal(ns)

	// Unique table name so parallel test runs don't collide.
	tblName := fmt.Sprintf("wgtest%d", time.Now().UnixNano()%1_000_000)

	// Missing table → empty, no error.
	got, err := r.NFTList(tblName)
	if err != nil {
		t.Fatalf("NFTList missing: %v", err)
	}
	if got != "" {
		t.Errorf("missing table returned %q", got)
	}

	// Apply a minimal ruleset creating the table.
	ruleset := fmt.Sprintf(`table inet %s {
	chain input {
		type filter hook input priority 0; policy accept;
	}
}
`, tblName)
	if err := r.NFTApply(ruleset); err != nil {
		t.Fatalf("NFTApply: %v", err)
	}

	got, err = r.NFTList(tblName)
	if err != nil {
		t.Fatalf("NFTList after apply: %v", err)
	}
	if !strings.Contains(got, "chain input") {
		t.Errorf("NFTList missing our chain:\n%s", got)
	}

	// Atomic reload: delete + redefine. nft -f treats the whole blob as one
	// transaction, so the table never disappears from a caller's POV.
	// `flush table` only empties content; to drop a chain we delete the
	// whole table and recreate it.
	ruleset2 := fmt.Sprintf(`delete table inet %s
table inet %s {
	chain forward {
		type filter hook forward priority 0; policy drop;
	}
}
`, tblName, tblName)
	if err := r.NFTApply(ruleset2); err != nil {
		t.Fatalf("NFTApply replace: %v", err)
	}
	got, _ = r.NFTList(tblName)
	if strings.Contains(got, "chain input") {
		t.Error("old chain still present after replace")
	}
	if !strings.Contains(got, "chain forward") {
		t.Errorf("new chain missing:\n%s", got)
	}
}
