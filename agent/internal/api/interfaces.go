package api

import (
	"net/http"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/model"
)

// interfaceListItem is the bulk-mode item: DB record + optional live kernel
// status. Mirrors the shape of /interfaces/{name} so the client can reuse
// the same type when asking for the bulk form.
type interfaceListItem struct {
	model.Interface
	Status      *kernel.InterfaceStatus `json:"status,omitempty"`
	StatusError string                  `json:"status_error,omitempty"`
}

// listInterfaces: GET /interfaces[?include=status]
// Without `include=status` — plain DB list (legacy shape). With it — each
// item is enriched with live kernel status (mirroring /interfaces/{name}).
// Saves the client an N+1 round-trip when it wants to render a live
// dashboard.
func (s *Server) listInterfaces(w http.ResponseWriter, r *http.Request) {
	ifaces, err := s.Store.ListInterfaces(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if r.URL.Query().Get("include") != "status" {
		writeJSON(w, http.StatusOK, ifaces)
		return
	}
	out := make([]interfaceListItem, 0, len(ifaces))
	for _, i := range ifaces {
		item := interfaceListItem{Interface: i}
		st, kerr := s.Kernel.ShowInterface(i.Name)
		if kerr == nil {
			item.Status = &st
		} else {
			item.StatusError = kerr.Error()
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

// showInterface returns the DB record enriched with live kernel status.
func (s *Server) showInterface(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	iface, err := s.Store.GetInterfaceByName(r.Context(), name)
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	status, kerr := s.Kernel.ShowInterface(name)
	resp := map[string]any{
		"interface": iface,
	}
	if kerr == nil {
		resp["status"] = status
	} else {
		resp["status_error"] = kerr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}
