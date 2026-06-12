package geoip

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// A disabled resolver — no path, or a path whose file doesn't exist — must be a
// safe no-op: it never errors at Open, reports Enabled()==false, and every
// Lookup misses. Geo is optional and must not block agent startup.
func TestDisabledResolverIsNoOp(t *testing.T) {
	cases := map[string]string{
		"empty path":   "",
		"missing file": filepath.Join(t.TempDir(), "nope.mmdb"),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			g, err := Open(path)
			if err != nil {
				t.Fatalf("Open(%q): unexpected error %v", path, err)
			}
			if g.Enabled() {
				t.Error("Enabled() = true for a resolver with no DB")
			}
			if db := g.Database(); db != "" {
				t.Errorf("Database() = %q, want empty", db)
			}
			if _, ok := g.Lookup(netip.MustParseAddr("8.8.8.8")); ok {
				t.Error("Lookup succeeded on a disabled resolver")
			}
			if err := g.Close(); err != nil {
				t.Errorf("Close(): %v", err)
			}
		})
	}
}

// A corrupt file must surface as a real error so the operator notices a
// misconfiguration rather than silently losing geo.
func TestOpenCorruptFileErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.mmdb")
	if err := os.WriteFile(p, []byte("not an mmdb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(p); err == nil {
		t.Error("Open on a corrupt file returned nil error")
	}
}

// The nil *Resolver must be callable without panicking — it's what a Server
// constructed without geo carries.
func TestNilResolverSafe(t *testing.T) {
	var g *Resolver
	if g.Enabled() {
		t.Error("nil Enabled() = true")
	}
	if _, ok := g.Lookup(netip.MustParseAddr("1.1.1.1")); ok {
		t.Error("nil Lookup succeeded")
	}
	if err := g.Close(); err != nil {
		t.Errorf("nil Close(): %v", err)
	}
	_ = g.Database()
	_ = g.Path()
}

func TestLocalizedPrefersRussian(t *testing.T) {
	got := localized(map[string]string{"en": "Moscow", "ru": "Москва"})
	if got != "Москва" {
		t.Errorf("localized = %q, want Москва", got)
	}
	if got := localized(map[string]string{"en": "Berlin"}); got != "Berlin" {
		t.Errorf("localized en-fallback = %q, want Berlin", got)
	}
	if got := localized(nil); got != "" {
		t.Errorf("localized(nil) = %q, want empty", got)
	}
}
