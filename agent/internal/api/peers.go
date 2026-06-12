package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/model"
	"github.com/gen1nya/wg-admin/agent/internal/renderer"
	"github.com/gen1nya/wg-admin/agent/internal/store"
	"github.com/gen1nya/wg-admin/agent/internal/wgkey"
)

type createPeerReq struct {
	Name          string `json:"name"`
	Address       string `json:"address,omitempty"` // "10.8.1.5/32" — optional, auto-assigned
	DefaultExitID *int64 `json:"default_exit_id,omitempty"`
	Notes         string `json:"notes,omitempty"`
	Tags          string `json:"tags,omitempty"` // raw JSON array as string
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
	// New peers get a preshared key by default — it's a free symmetric hardening
	// layer and matches the existing fleet (the imported wg-easy peers had one).
	psk, err := wgkey.GenPSK()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "genpsk: "+err.Error())
		return
	}

	tags := req.Tags
	if tags == "" {
		tags = "[]"
	}

	// Allocate/validate the address and insert under peerMu so two concurrent
	// creates can't pick the same /32. Everything that reads "what's free" and
	// then writes must be inside this critical section.
	s.peerMu.Lock()
	addr, code, err := s.resolvePeerAddress(ctx, iface, req.Address)
	if err != nil {
		s.peerMu.Unlock()
		writeErr(w, code, err.Error())
		return
	}
	peer := &model.Peer{
		InterfaceID:   iface.ID,
		Name:          req.Name,
		PublicKey:     pub,
		PrivateKey:    priv,
		PresharedKey:  psk,
		Address:       addr,
		DefaultExitID: req.DefaultExitID,
		Enabled:       true,
		Notes:         req.Notes,
		Tags:          tags,
		CreatedAt:     time.Now().Unix(),
	}
	id, err := s.Store.InsertPeer(ctx, peer)
	s.peerMu.Unlock()
	if err != nil {
		// The unique index (migration 0005) turns a lost race into a clean
		// conflict instead of a silent duplicate.
		if isUniqueViolation(err) {
			writeErr(w, http.StatusConflict, "address "+addr+" already in use on "+iface.Name)
			return
		}
		writeErr(w, http.StatusInternalServerError, "insert: "+err.Error())
		return
	}
	peer.ID = id

	if err := s.Kernel.SetPeer(iface.Name, pub, addr, psk); err != nil {
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

// resolvePeerAddress returns the canonical "ip/mask" string to store for a new
// peer. With an empty request it auto-allocates the next free host address;
// with an explicit one it validates the address is a host route inside the
// interface subnet, isn't the interface's own address, and isn't taken.
// Must be called with peerMu held. The int is the HTTP status to use on error.
func (s *Server) resolvePeerAddress(ctx context.Context, iface model.Interface, requested string) (string, int, error) {
	if strings.TrimSpace(requested) == "" {
		a, err := s.Store.NextFreeAddress(ctx, iface)
		if err != nil {
			return "", http.StatusConflict, err
		}
		return a, 0, nil
	}
	addr, err := validateClientAddress(iface, requested)
	if err != nil {
		return "", http.StatusBadRequest, err
	}
	taken, err := s.Store.AddressTaken(ctx, iface.ID, addr)
	if err != nil {
		return "", http.StatusInternalServerError, err
	}
	if taken {
		return "", http.StatusConflict, fmt.Errorf("address %s already in use on %s", addr, iface.Name)
	}
	return addr, 0, nil
}

// validateClientAddress canonicalises and checks an operator-supplied client
// address. It must be a single host (/32 for IPv4, /128 for IPv6) inside the
// interface subnet and distinct from the interface's own address — otherwise a
// caller could pass another peer's /32 (kernel moves the allowed-ip, cutting
// that client off) or 0.0.0.0/0 (peer captures all interface traffic).
func validateClientAddress(iface model.Interface, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	host, err := netip.ParseAddr(requested)
	if err != nil {
		p, perr := netip.ParsePrefix(requested)
		if perr != nil {
			return "", fmt.Errorf("invalid address %q: not an IP or CIDR", requested)
		}
		hostBits := 32
		if p.Addr().Is6() {
			hostBits = 128
		}
		if p.Bits() != hostBits {
			return "", fmt.Errorf("client address must be a single host (/%d), got %s", hostBits, requested)
		}
		host = p.Addr()
	}
	host = host.Unmap()

	subnet, err := netip.ParsePrefix(iface.Subnet)
	if err != nil {
		return "", fmt.Errorf("interface subnet %q unparseable: %w", iface.Subnet, err)
	}
	if host.Is4() != subnet.Addr().Is4() {
		return "", fmt.Errorf("address %s family differs from interface subnet %s", host, iface.Subnet)
	}
	if !subnet.Contains(host) {
		return "", fmt.Errorf("address %s is outside interface subnet %s", host, iface.Subnet)
	}
	if ifAddr, err := netip.ParsePrefix(iface.Address); err == nil && ifAddr.Addr().Unmap() == host {
		return "", fmt.Errorf("address %s is the interface's own address", host)
	}

	bits := 32
	if host.Is6() {
		bits = 128
	}
	return fmt.Sprintf("%s/%d", host, bits), nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE-constraint failure.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
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

	// Toggling `enabled` must reach the kernel, not just the DB — otherwise a
	// "disabled" (revoked) client keeps its tunnel until the next agent restart,
	// and an "enabled" one stays absent. Only clients interfaces are touched;
	// mesh peers are out of tier-1 scope. A kernel failure is surfaced (500) so
	// the operator knows the revoke/enable didn't take; the DB already records
	// the intent and boot-reconcile heals the drift.
	if patch.Enabled != nil {
		if iface, ierr := s.Store.GetInterface(r.Context(), updated.InterfaceID); ierr == nil && iface.Role == model.RoleClients {
			var kerr error
			if *patch.Enabled {
				kerr = s.Kernel.SetPeer(iface.Name, updated.PublicKey, updated.Address, updated.PresharedKey)
			} else {
				kerr = s.Kernel.RemovePeer(iface.Name, updated.PublicKey)
			}
			if kerr != nil {
				slog.Error("kernel peer toggle failed", "err", kerr, "iface", iface.Name, "enabled", *patch.Enabled)
				payload, _ := json.Marshal(patch)
				_ = s.Store.LogAudit(r.Context(), audActor(r), "peer.update", "peer", &id, string(payload))
				writeErr(w, http.StatusInternalServerError, "kernel: "+kerr.Error())
				return
			}
		}
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
