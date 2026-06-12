// Package geoip resolves IP addresses to coarse location using an offline
// MaxMind-format database (GeoLite2-City or -Country, or any .mmdb with the
// same record shape). It never makes network calls — the only input is the
// local DB file — so it works air-gapped and leaks no client IPs to third
// parties.
//
// Geo is optional: if no DB is configured or the file is absent, the resolver
// degrades to a no-op (Enabled() == false, every Lookup misses) and the agent
// starts normally. Only a present-but-corrupt DB is a hard error.
package geoip

import (
	"errors"
	"net/netip"
	"os"
	"sync"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Info is the subset of a GeoLite2 record we surface. With a Country-level DB
// (or an entry that carries no coordinates) HasLocation is false and Lat/Lon
// are zero — the caller can still place it by CountryCode or list it apart.
type Info struct {
	Country     string  // localized name (ru, then en); may be ""
	CountryCode string  // ISO 3166-1 alpha-2, e.g. "RU"
	City        string  // localized name; may be ""
	Lat         float64 // 0 when HasLocation is false
	Lon         float64
	HasLocation bool
	Accuracy    uint16 // accuracy_radius in km; 0 if the DB omits it
}

// Resolver wraps a maxminddb reader behind an RWMutex. The nil *Resolver and a
// resolver opened against a missing file are both valid no-ops. The underlying
// reader is safe for concurrent lookups; the mutex only guards Close/reopen.
type Resolver struct {
	mu   sync.RWMutex
	r    *maxminddb.Reader
	path string
}

// Open loads the mmdb at path. An empty path or a missing file yields a
// disabled resolver and a nil error (geo is optional). A corrupt or unreadable
// file is returned as an error so the operator notices a real misconfiguration.
func Open(path string) (*Resolver, error) {
	if path == "" {
		return &Resolver{}, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return &Resolver{path: path}, nil
	}
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &Resolver{r: r, path: path}, nil
}

// Enabled reports whether a database is loaded.
func (g *Resolver) Enabled() bool {
	if g == nil {
		return false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.r != nil
}

// Database returns the mmdb's declared type (e.g. "GeoLite2-City"), or "".
func (g *Resolver) Database() string {
	if g == nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.r == nil {
		return ""
	}
	return g.r.Metadata.DatabaseType
}

// Path returns the configured DB path even when the file was absent at open
// time — handy for telling the operator where to drop the database.
func (g *Resolver) Path() string {
	if g == nil {
		return ""
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.path
}

type record struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Location struct {
		Latitude       float64 `maxminddb:"latitude"`
		Longitude      float64 `maxminddb:"longitude"`
		AccuracyRadius uint16  `maxminddb:"accuracy_radius"`
	} `maxminddb:"location"`
}

// Lookup resolves ip to geo info. The bool is false for a disabled resolver, an
// invalid/private/loopback address, a DB miss, or a decode failure. Private
// space is skipped deliberately: a peer's endpoint there (CGNAT-internal, mesh)
// carries no public geo signal.
func (g *Resolver) Lookup(ip netip.Addr) (Info, bool) {
	if g == nil {
		return Info{}, false
	}
	g.mu.RLock()
	r := g.r
	g.mu.RUnlock()
	if r == nil || !ip.IsValid() {
		return Info{}, false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return Info{}, false
	}
	res := r.Lookup(ip)
	if res.Err() != nil || !res.Found() {
		return Info{}, false
	}
	var rec record
	if err := res.Decode(&rec); err != nil {
		return Info{}, false
	}
	info := Info{
		Country:     localized(rec.Country.Names),
		CountryCode: rec.Country.ISOCode,
		City:        localized(rec.City.Names),
		Lat:         rec.Location.Latitude,
		Lon:         rec.Location.Longitude,
		Accuracy:    rec.Location.AccuracyRadius,
	}
	// A real coordinate is the only reliable "has location" signal; the literal
	// 0,0 (Null Island) never legitimately appears in GeoLite2.
	info.HasLocation = info.Lat != 0 || info.Lon != 0
	if info.CountryCode == "" && !info.HasLocation {
		return Info{}, false
	}
	return info, true
}

// localized prefers the Russian name (the UI is Russian) and falls back to
// English, then to any available name.
func localized(names map[string]string) string {
	if names == nil {
		return ""
	}
	if v := names["ru"]; v != "" {
		return v
	}
	if v := names["en"]; v != "" {
		return v
	}
	for _, v := range names {
		if v != "" {
			return v
		}
	}
	return ""
}

// Close releases the database. Safe to call on a disabled resolver.
func (g *Resolver) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.r == nil {
		return nil
	}
	err := g.r.Close()
	g.r = nil
	return err
}
