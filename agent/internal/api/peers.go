package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/renderer"
	"github.com/gen1nya/wg-admin/agent/internal/store"
	"github.com/gen1nya/wg-admin/agent/internal/wgkey"
)

type createPeerReq struct {
	Name          string  `json:"name"`
	Address       string  `json:"address,omitempty"` // "10.8.1.5/32" — optional, auto-assigned
	DefaultExitID *int64  `json:"default_exit_id,omitempty"`
	Notes         string  `json:"notes,omitempty"`
	Tags          string  `json:"tags,omitempty"` // raw JSON array as string
}

func (s *Server) listPeers(w http.ResponseWriter, r *http.Request) {
	peers, err := s.Store.ListPeers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, peers)
}

func (s *Server) listPeersOnInterface(w http.ResponseWriter, r *http.Request) {
	iface, err := s.Store.GetInterfaceByName(r.Context(), r.PathValue("name"))
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	peers, err := s.Store.ListPeersByInterface(r.Context(), iface.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, peers)
}

func (s *Server) getPeer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	p, err := s.Store.GetPeer(r.Context(), id)
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) createPeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	iface, err := s.Store.GetInterfaceByName(ctx, r.PathValue("name"))
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	if iface.Role != model.RoleClients {
		writeErr(w, http.StatusConflict, fmt.Sprintf("interface %q has role %q; only 'clients' accepts peer CRUD", iface.Name, iface.Role))
		return
	}

	var req createPeerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}

	priv, pub, err := wgkey.GenPair()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "keygen: "+err.Error())
		return
	}

	addr := req.Address
	if addr == "" {
		a, err := s.Store.NextFreeAddress(ctx, iface)
		if err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		addr = a
	}

	tags := req.Tags
	if tags == "" {
		tags = "[]"
	}

	peer := &model.Peer{
		InterfaceID:   iface.ID,
		Name:          req.Name,
		PublicKey:     pub,
		PrivateKey:    priv,
		Address:       addr,
		DefaultExitID: req.DefaultExitID,
		Enabled:       true,
		Notes:         req.Notes,
		Tags:          tags,
		CreatedAt:     time.Now().Unix(),
	}

	id, err := s.Store.InsertPeer(ctx, peer)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "insert: "+err.Error())
		return
	}
	peer.ID = id

	if err := s.Kernel.SetPeer(iface.Name, pub, addr); err != nil {
		// rollback DB row — keep DB/kernel in sync
		if delErr := s.Store.DeletePeer(ctx, id); delErr != nil {
			slog.Error("rollback after kernel failure", "err", delErr, "orig", err)
		}
		writeErr(w, http.StatusInternalServerError, "kernel: "+err.Error())
		return
	}

	payload, _ := json.Marshal(map[string]any{"public_key": pub, "address": addr})
	if err := s.Store.LogAudit(ctx, audActor(r), "peer.create", "peer", &id, string(payload)); err != nil {
		slog.Warn("audit log", "err", err)
	}

	writeJSON(w, http.StatusCreated, peer)
}

func (s *Server) updatePeer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var patch store.PeerPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if err := s.Store.UpdatePeer(r.Context(), id, patch); err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	updated, err := s.Store.GetPeer(r.Context(), id)
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	payload, _ := json.Marshal(patch)
	_ = s.Store.LogAudit(r.Context(), audActor(r), "peer.update", "peer", &id, string(payload))
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deletePeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	peer, err := s.Store.GetPeer(ctx, id)
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	iface, err := s.Store.GetInterface(ctx, peer.InterfaceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if iface.Role != model.RoleClients {
		writeErr(w, http.StatusConflict, fmt.Sprintf("interface %q has role %q; refusing to delete mesh peer via tier-1 API", iface.Name, iface.Role))
		return
	}
	if err := s.Kernel.RemovePeer(iface.Name, peer.PublicKey); err != nil {
		// record but don't block DB delete — kernel may have lost the peer already
		slog.Warn("kernel.RemovePeer failed", "err", err, "iface", iface.Name)
	}
	if err := s.Store.DeletePeer(ctx, id); err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	payload := fmt.Sprintf(`{"public_key":%q}`, peer.PublicKey)
	_ = s.Store.LogAudit(ctx, audActor(r), "peer.delete", "peer", &id, payload)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := s.Store.ListAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// audActor returns a short actor label. For now, just "cli" — once the app
// wraps requests with a JWT or similar, propagate the sub claim via header.
func audActor(r *http.Request) string {
	if v := r.Header.Get("X-Actor"); v != "" {
		return v
	}
	return "cli"
}

// getPeerConfig renders and returns the wg-quick style client .conf for the peer.
// Response shape: {"config": "[Interface]...", "peer_id": N}.
func (s *Server) getPeerConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	peer, err := s.Store.GetPeer(ctx, id)
	if err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	iface, err := s.Store.GetInterface(ctx, peer.InterfaceID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if iface.Role != model.RoleClients {
		writeErr(w, http.StatusConflict, fmt.Sprintf("interface %q has role %q; no client .conf to render", iface.Name, iface.Role))
		return
	}
	conf, err := renderer.ClientConfig(iface, peer)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "render: "+err.Error())
		return
	}
	// Support text/plain via ?format=raw or Accept header.
	if r.URL.Query().Get("format") == "raw" || r.Header.Get("accept") == "text/plain" {
		w.Header().Set("content-type", "text/plain; charset=utf-8")
		w.Header().Set("content-disposition",
			fmt.Sprintf(`attachment; filename="%s-%s.conf"`, iface.Name, sanitize(peer.Name)))
		_, _ = w.Write([]byte(conf))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peer_id": id,
		"config":  conf,
	})
}

// updatePeerExit assigns (or clears) the peer's default_exit_id.
// Body: {"exit_id": N} to set, {"exit_id": null} or {"clear": true} to inherit.
func (s *Server) updatePeerExit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		ExitID *int64 `json:"exit_id"`
		Clear  bool   `json:"clear"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if _, err := s.Store.GetPeer(ctx, id); err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}

	patch := store.PeerPatch{}
	if body.Clear || body.ExitID == nil {
		patch.ClearExit = true
	} else {
		// validate exit exists and is enabled
		ex, err := s.Store.GetExit(ctx, *body.ExitID)
		if err != nil {
			writeErr(w, statusForErr(err), "exit not found")
			return
		}
		if !ex.Enabled {
			writeErr(w, http.StatusBadRequest, "exit is disabled")
			return
		}
		patch.DefaultExitID = body.ExitID
	}
	if err := s.Store.UpdatePeer(ctx, id, patch); err != nil {
		writeErr(w, statusForErr(err), err.Error())
		return
	}
	updated, _ := s.Store.GetPeer(ctx, id)
	payload, _ := json.Marshal(body)
	_ = s.Store.LogAudit(ctx, audActor(r), "peer.exit", "peer", &id, string(payload))
	writeJSON(w, http.StatusOK, updated)
}

// sanitize strips path-unfriendly chars from the peer name for download filenames.
func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_' || r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "peer"
	}
	return string(out)
}
