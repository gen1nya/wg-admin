package api

import (
	"errors"
	"net/http"

	"github.com/gen1nya/wg-admin/agent/internal/store"
)

type trafficEntry struct {
	PeerID          int64  `json:"peer_id,omitempty"`
	Interface       string `json:"interface"`
	PeerName        string `json:"peer_name,omitempty"`
	PublicKey       string `json:"public_key"`
	AllowedIPs      string `json:"allowed_ips"`
	RxBytes         int64  `json:"rx_bytes"`
	TxBytes         int64  `json:"tx_bytes"`
	LatestHandshake int64  `json:"latest_handshake"`
	Unknown         bool   `json:"unknown,omitempty"` // kernel peer not in DB
}

// listTraffic aggregates live per-peer counters from the kernel and joins them
// with the DB peer records. Peers present in kernel but not DB are returned
// with unknown=true so the UI can flag drift.
func (s *Server) listTraffic(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ifaces, err := s.Store.ListInterfaces(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var out []trafficEntry
	for _, iface := range ifaces {
		st, err := s.Kernel.ShowInterface(iface.Name)
		if err != nil {
			// skip missing interfaces; don't fail the whole response
			continue
		}
		for _, kp := range st.Peers {
			entry := trafficEntry{
				Interface:       iface.Name,
				PublicKey:       kp.PublicKey,
				AllowedIPs:      kp.AllowedIPs,
				RxBytes:         kp.RxBytes,
				TxBytes:         kp.TxBytes,
				LatestHandshake: kp.LatestHandshake,
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
			out = append(out, entry)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
