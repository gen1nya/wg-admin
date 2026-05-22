package importer_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gen1nya/wg-admin/agent/internal/importer"
	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/renderer"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// liveFixtureDir returns a path to a directory with real /etc/wireguard
// backup files for an end-to-end importer test, or "" if not configured.
//
// Set WG_ADMIN_IMPORTER_FIXTURE to point at a directory laid out like
// /etc/wireguard (wg0.conf, wg-*.conf, plus optional clients/ and
// clients-<iface>/ subdirs holding per-peer private keys). Tests that
// need this skip when the env var is unset, so CI without the fixture
// stays green.
func liveFixtureDir() string {
	return os.Getenv("WG_ADMIN_IMPORTER_FIXTURE")
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "import.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestImportBackup(t *testing.T) {
	d := liveFixtureDir()
	if d == "" {
		t.Skip("WG_ADMIN_IMPORTER_FIXTURE not set")
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}
	st := openTestStore(t)
	stats, err := importer.Run(context.Background(), st, importer.Options{
		FromDir:    d,
		PublicHost: "vpn.example.com",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if stats.Interfaces < 5 {
		t.Errorf("interfaces=%d, want >=5", stats.Interfaces)
	}
	if stats.Peers < 13 {
		t.Errorf("peers=%d, want >=13 (wg0 alone has 13)", stats.Peers)
	}

	ctx := context.Background()
	ifaces, _ := st.ListInterfaces(ctx)
	byName := map[string]bool{}
	for _, i := range ifaces {
		byName[i.Name] = true
	}
	for _, n := range []string{"wg0", "wg-clients-b"} {
		if !byName[n] {
			t.Errorf("interface %q not imported", n)
		}
	}

	wg0, err := st.GetInterfaceByName(ctx, "wg0")
	if err != nil {
		t.Fatalf("get wg0: %v", err)
	}
	if wg0.Subnet != "10.8.1.0/24" {
		t.Errorf("wg0 subnet=%q", wg0.Subnet)
	}
	if wg0.PublicEndpoint != "vpn.example.com" {
		t.Errorf("wg0 public_endpoint=%q", wg0.PublicEndpoint)
	}
	peers, _ := st.ListPeersByInterface(ctx, wg0.ID)
	if len(peers) != 13 {
		t.Errorf("wg0 peers=%d, want 13", len(peers))
	}
	namedCount := 0
	keyedCount := 0
	for _, p := range peers {
		if p.Name != "" {
			namedCount++
		}
		if p.PrivateKey != "" {
			keyedCount++
		}
	}
	if namedCount != 13 {
		t.Errorf("named peers=%d, want 13", namedCount)
	}
	if keyedCount != 13 {
		t.Errorf("keyed peers=%d, want 13 (all wg0 clients had saved keys)", keyedCount)
	}

	// Role heuristic on the real backup: wg0 + wg-clients-b serve clients,
	// wg-mesh + wg-mesh-home + wg-test-b are mesh tunnels (single peer
	// with 0.0.0.0/0 or a remote subnet).
	expectRole := map[string]string{
		"wg0":           "clients",
		"wg-clients-b": "clients",
		"wg-mesh":       "mesh",
		"wg-mesh-home": "mesh",
		"wg-test-b":    "mesh",
	}
	for name, want := range expectRole {
		iface, err := st.GetInterfaceByName(ctx, name)
		if err != nil {
			t.Errorf("get %s: %v", name, err)
			continue
		}
		if iface.Role != want {
			t.Errorf("%s role=%q, want %q", name, iface.Role, want)
		}
	}

	// Render a .conf for one imported client peer end-to-end. We pick a
	// peer whose private key was harvested from the clients/ sidecar.
	var sample *model.Peer
	for i := range peers {
		if peers[i].PrivateKey != "" {
			sample = &peers[i]
			break
		}
	}
	if sample == nil {
		t.Fatal("no peer with private key to render")
	}
	conf, err := renderer.ClientConfig(wg0, *sample)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, sub := range []string{
		"[Interface]",
		"PrivateKey = " + sample.PrivateKey,
		"Address = " + sample.Address,
		"[Peer]",
		"Endpoint = vpn.example.com:",
		"PublicKey = ",
	} {
		if !strings.Contains(conf, sub) {
			t.Errorf("rendered conf missing %q:\n%s", sub, conf)
		}
	}
	t.Logf("sample rendered conf for %q on wg0:\n%s", sample.Name, conf)
}

// TestImportSingleFile verifies the adoption workflow: import one .conf
// (no sibling clients/ dir) and check the interface row lands in the DB.
func TestImportSingleFile(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "wg-stub.conf")
	conf := `[Interface]
Address = 10.99.1.1/24
ListenPort = 51870
PrivateKey = oCXf1TfyXY490dAvi8JgRstqHSHg4SQgErjfeEwH3V4=
`
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	st := openTestStore(t)
	ctx := context.Background()
	stats, err := importer.Run(ctx, st, importer.Options{
		FromDir:    confPath,
		PublicHost: "stub.local",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if stats.Interfaces != 1 || stats.Peers != 0 {
		t.Errorf("stats=%+v, want {Interfaces:1 Peers:0}", stats)
	}

	iface, err := st.GetInterfaceByName(ctx, "wg-stub")
	if err != nil {
		t.Fatalf("get iface: %v", err)
	}
	if iface.Address != "10.99.1.1/24" || iface.Subnet != "10.99.1.0/24" {
		t.Errorf("addr/subnet: %+v", iface)
	}
	if iface.ListenPort != 51870 {
		t.Errorf("port=%d", iface.ListenPort)
	}
	if iface.PublicEndpoint != "stub.local" {
		t.Errorf("public_endpoint=%q", iface.PublicEndpoint)
	}
}

// TestImportDetectsMeshRole: a server conf whose single peer uses 0.0.0.0/0
// is a mesh tunnel, not a client-serving interface.
func TestImportDetectsMeshRole(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "wg-mesh.conf")
	conf := `[Interface]
Address = 10.99.0.2/30
ListenPort = 51820
PrivateKey = oCXf1TfyXY490dAvi8JgRstqHSHg4SQgErjfeEwH3V4=

[Peer]
PublicKey = o++zwh6pNiv9d3PsrQC1e6C5YLz8o56gYh3njI0v73U=
AllowedIPs = 0.0.0.0/0
Endpoint = vpn.example.com:51820
`
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := importer.Run(ctx, st, importer.Options{FromDir: confPath}); err != nil {
		t.Fatalf("import: %v", err)
	}
	iface, err := st.GetInterfaceByName(ctx, "wg-mesh")
	if err != nil {
		t.Fatalf("get iface: %v", err)
	}
	if iface.Role != "mesh" {
		t.Errorf("role=%q, want mesh", iface.Role)
	}
}

func TestImportDetectsClientsRole(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "wg0.conf")
	conf := `[Interface]
Address = 10.8.1.1/24
ListenPort = 51820
PrivateKey = oCXf1TfyXY490dAvi8JgRstqHSHg4SQgErjfeEwH3V4=

[Peer]
PublicKey = o++zwh6pNiv9d3PsrQC1e6C5YLz8o56gYh3njI0v73U=
AllowedIPs = 10.8.1.5/32

[Peer]
PublicKey = yROGUixfS6n++jkglzyY+PVAl8nXzg8mvZ8wZI5nQSI=
AllowedIPs = 10.8.1.6/32
`
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := importer.Run(ctx, st, importer.Options{FromDir: confPath}); err != nil {
		t.Fatalf("import: %v", err)
	}
	iface, err := st.GetInterfaceByName(ctx, "wg0")
	if err != nil {
		t.Fatalf("get iface: %v", err)
	}
	if iface.Role != "clients" {
		t.Errorf("role=%q, want clients", iface.Role)
	}
}

func TestImportSingleFileRejectsNonConf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-conf.txt")
	if err := os.WriteFile(path, []byte("junk"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	st := openTestStore(t)
	_, err := importer.Run(context.Background(), st, importer.Options{FromDir: path})
	if err == nil {
		t.Error("expected error on non-.conf file")
	}
}

func TestImportIsIdempotent(t *testing.T) {
	d := liveFixtureDir()
	if d == "" {
		t.Skip("WG_ADMIN_IMPORTER_FIXTURE not set")
	}
	if _, err := os.Stat(d); err != nil {
		t.Skipf("fixture unavailable: %v", err)
	}
	st := openTestStore(t)
	ctx := context.Background()
	opt := importer.Options{FromDir: d, PublicHost: "vpn.example.com"}
	_, err := importer.Run(ctx, st, opt)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	peers1, _ := st.ListPeers(ctx)
	// Second run must not add duplicates.
	if _, err := importer.Run(ctx, st, opt); err != nil {
		t.Fatalf("second run: %v", err)
	}
	peers2, _ := st.ListPeers(ctx)
	if len(peers1) != len(peers2) {
		t.Errorf("peers changed after re-import: %d → %d", len(peers1), len(peers2))
	}
}
