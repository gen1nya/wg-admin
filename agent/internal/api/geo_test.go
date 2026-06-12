package api_test

import (
	"strings"
	"testing"
)

// With no geo DB configured (the test server carries a nil resolver), /geo must
// still answer: enabled=false, and every connected kernel peer appears with its
// endpoint IP parsed out but no coordinates. Secrets must never leak.
func TestGeoDisabledStillListsEndpoints(t *testing.T) {
	ts := newTestServer(t)

	w := ts.do(t, "GET", "/geo", nil)
	if w.Code != 200 {
		t.Fatalf("geo: %d %s", w.Code, w.Body.String())
	}

	type entry struct {
		Interface   string  `json:"interface"`
		PublicKey   string  `json:"public_key"`
		Endpoint    string  `json:"endpoint"`
		EndpointIP  string  `json:"endpoint_ip"`
		HasLocation bool    `json:"has_location"`
		Lat         float64 `json:"lat"`
		Unknown     bool    `json:"unknown"`
	}
	type geoResp struct {
		Enabled bool    `json:"enabled"`
		Entries []entry `json:"entries"`
	}
	resp := decode[geoResp](t, w)

	if resp.Enabled {
		t.Error("enabled=true with no DB configured")
	}

	// The mock seeds wg0 with one peer at 203.0.113.10:51820. Find it.
	var got *entry
	for i := range resp.Entries {
		if resp.Entries[i].Interface == "wg0" {
			got = &resp.Entries[i]
		}
	}
	if got == nil {
		t.Fatalf("wg0 peer missing from geo entries: %s", w.Body.String())
	}
	if got.EndpointIP != "203.0.113.10" {
		t.Errorf("endpoint_ip = %q, want 203.0.113.10", got.EndpointIP)
	}
	if got.HasLocation {
		t.Error("has_location=true with geo disabled")
	}
}

// Secrets must not appear in the /geo payload under any key.
func TestGeoDoesNotLeakSecrets(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(t, "GET", "/geo", nil)
	body := w.Body.String()
	for _, bad := range []string{"private", "preshared"} {
		if strings.Contains(body, bad) {
			t.Errorf("geo payload leaks %q: %s", bad, body)
		}
	}
}
