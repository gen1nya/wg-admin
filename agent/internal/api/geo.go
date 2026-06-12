package api

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// geoEntry is one connected peer with its endpoint resolved to a location.
// Secrets (private/preshared keys) are never included. When geo is disabled or
// the endpoint can't be located, HasLocation is false and Lat/Lon are omitted —
// the entry is still returned so the UI can list it apart from the map.
type geoEntry struct {
	PeerID          int64   `json:"peer_id,omitempty"`
	PeerName        string  `json:"peer_name,omitempty"`
	Interface       string  `json:"interface"`
	Role            string  `json:"role"`
	PublicKey       string  `json:"public_key"`
	Endpoint        string  `json:"endpoint"`     // raw host:port from the kernel
	EndpointIP      string  `json:"endpoint_ip"`  // the IP part only
	Country         string  `json:"country,omitempty"`
	CountryCode     string  `json:"country_code,omitempty"`
	City            string  `json:"city,omitempty"`
	Lat             float64 `json:"lat,omitempty"`
	Lon             float64 `json:"lon,omitempty"`
	HasLocation     bool    `json:"has_location"`
	AccuracyKm      uint16  `json:"accuracy_km,omitempty"`
	LatestHandshake int64   `json:"latest_handshake"`
	RxBytes         int64   `json:"rx_bytes"`
	TxBytes         int64   `json:"tx_bytes"`
	// RTTms is the tunnel-ping round trip (ms) when the peer answered ICMP on
	// its tunnel IP; omitted otherwise (timeout, never probed, or — commonly —
	// a Windows client that drops echo). RTTAgeSec is how stale that sample is.
	RTTms     float64 `json:"rtt_ms,omitempty"`
	RTTAgeSec int64   `json:"rtt_age_sec,omitempty"`
	Unknown   bool    `json:"unknown,omitempty"` // kernel peer absent from DB
}

type geoResponse struct {
	Enabled  bool       `json:"enabled"`            // a geo DB is loaded
	Database string     `json:"database,omitempty"` // mmdb type, e.g. GeoLite2-City
	DBPath   string     `json:"db_path,omitempty"`  // where to drop the DB if absent
	Entries  []geoEntry `json:"entries"`
}

// listGeo enumerates every peer that currently has an endpoint (i.e. has
// connected at least once) and resolves that endpoint IP to a coarse location
// via the offline geo DB. Peers that never connected (no endpoint) are omitted:
// the map is about where clients are, and they have no IP to place.
func (s *Server) listGeo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ifaces, err := s.Store.ListInterfaces(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := geoResponse{
		Enabled:  s.Geo.Enabled(),
		Database: s.Geo.Database(),
		DBPath:   s.Geo.Path(),
		Entries:  []geoEntry{},
	}
	now := time.Now().Unix()

	for _, iface := range ifaces {
		st, err := s.Kernel.ShowInterface(iface.Name)
		if err != nil {
			continue // skip interfaces missing from the kernel
		}
		for _, kp := range st.Peers {
			if kp.Endpoint == "" {
				continue // never connected — nothing to locate
			}
			entry := geoEntry{
				Interface:       iface.Name,
				Role:            iface.Role,
				PublicKey:       kp.PublicKey,
				Endpoint:        kp.Endpoint,
				LatestHandshake: kp.LatestHandshake,
				RxBytes:         kp.RxBytes,
				TxBytes:         kp.TxBytes,
			}
			if ip, ok := endpointIP(kp.Endpoint); ok {
				entry.EndpointIP = ip.String()
				if info, found := s.Geo.Lookup(ip); found {
					entry.Country = info.Country
					entry.CountryCode = info.CountryCode
					entry.City = info.City
					entry.Lat = info.Lat
					entry.Lon = info.Lon
					entry.HasLocation = info.HasLocation
					entry.AccuracyKm = info.Accuracy
				}
			}

			if smp, ok := s.RTT.Get(kp.PublicKey); ok && smp.OK {
				entry.RTTms = smp.RTTms
				entry.RTTAgeSec = now - smp.MeasuredAt
			}

			dbPeer, err := s.Store.GetPeerByPublicKey(ctx, kp.PublicKey)
			switch {
			case err == nil:
				entry.PeerID = dbPeer.ID
				entry.PeerName = dbPeer.Name
			case errors.Is(err, store.ErrNotFound):
				entry.Unknown = true
			default:
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			resp.Entries = append(resp.Entries, entry)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// endpointIP extracts the IP from a "host:port" WireGuard endpoint, handling
// the bracketed IPv6 form. Returns ok=false if it isn't a parseable IP.
func endpointIP(endpoint string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint // tolerate a bare IP without a port
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
