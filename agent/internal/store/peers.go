package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"

	"github.com/gen1nya/wg-admin/agent/internal/model"
)

const peerCols = `id, interface_id, name, public_key, private_key, address,
	default_exit_id, enabled, notes, tags, created_at`

func scanPeer(row interface{ Scan(...any) error }) (model.Peer, error) {
	var p model.Peer
	var defaultExitID sql.NullInt64
	err := row.Scan(
		&p.ID, &p.InterfaceID, &p.Name, &p.PublicKey, &p.PrivateKey,
		&p.Address, &defaultExitID, &p.Enabled, &p.Notes, &p.Tags, &p.CreatedAt,
	)
	if err != nil {
		return p, err
	}
	if defaultExitID.Valid {
		p.DefaultExitID = &defaultExitID.Int64
	}
	return p, nil
}

func (s *Store) ListPeers(ctx context.Context) ([]model.Peer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+peerCols+` FROM peers ORDER BY interface_id, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListPeersByInterface(ctx context.Context, ifaceID int64) ([]model.Peer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+peerCols+` FROM peers WHERE interface_id=? ORDER BY name`, ifaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPeer(ctx context.Context, id int64) (model.Peer, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+peerCols+` FROM peers WHERE id=?`, id)
	p, err := scanPeer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) GetPeerByPublicKey(ctx context.Context, pubkey string) (model.Peer, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+peerCols+` FROM peers WHERE public_key=?`, pubkey)
	p, err := scanPeer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) InsertPeer(ctx context.Context, p *model.Peer) (int64, error) {
	var defaultExitID any
	if p.DefaultExitID != nil {
		defaultExitID = *p.DefaultExitID
	}
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO peers (interface_id, name, public_key, private_key, address,
			default_exit_id, enabled, notes, tags, created_at)
		VALUES (?,?,?,?,?, ?,?,?,?,?)`,
		p.InterfaceID, p.Name, p.PublicKey, p.PrivateKey, p.Address,
		defaultExitID, p.Enabled, p.Notes, p.Tags, p.CreatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// PeerPatch lists fields allowed for update via PATCH.
type PeerPatch struct {
	Name          *string `json:"name,omitempty"`
	Notes         *string `json:"notes,omitempty"`
	Tags          *string `json:"tags,omitempty"`
	DefaultExitID *int64  `json:"default_exit_id,omitempty"`
	ClearExit     bool    `json:"clear_exit,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
}

func (s *Store) UpdatePeer(ctx context.Context, id int64, patch PeerPatch) error {
	sets := []string{}
	args := []any{}
	if patch.Name != nil {
		sets = append(sets, "name=?")
		args = append(args, *patch.Name)
	}
	if patch.Notes != nil {
		sets = append(sets, "notes=?")
		args = append(args, *patch.Notes)
	}
	if patch.Tags != nil {
		sets = append(sets, "tags=?")
		args = append(args, *patch.Tags)
	}
	if patch.ClearExit {
		sets = append(sets, "default_exit_id=NULL")
	} else if patch.DefaultExitID != nil {
		sets = append(sets, "default_exit_id=?")
		args = append(args, *patch.DefaultExitID)
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled=?")
		args = append(args, *patch.Enabled)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	q := "UPDATE peers SET " + join(sets, ", ") + " WHERE id=?"
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddressTaken reports whether any peer on the interface already holds addr.
// Used to reject an explicit client address before INSERT; the unique index
// is the race-proof backstop.
func (s *Store) AddressTaken(ctx context.Context, ifaceID int64, addr string) (bool, error) {
	var one int
	err := s.DB.QueryRowContext(ctx,
		`SELECT 1 FROM peers WHERE interface_id=? AND address=? LIMIT 1`, ifaceID, addr).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) DeletePeer(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM peers WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// NextFreeAddress returns the next unused IPv4 address in the interface's
// subnet, formatted as "X.Y.Z.W/32". It reserves the interface's own
// address and all existing peer addresses.
func (s *Store) NextFreeAddress(ctx context.Context, iface model.Interface) (string, error) {
	prefix, err := netip.ParsePrefix(iface.Subnet)
	if err != nil {
		return "", fmt.Errorf("parse subnet %q: %w", iface.Subnet, err)
	}
	used := map[netip.Addr]bool{}

	// reserve the interface's own address
	if ifaceAddr, err := netip.ParsePrefix(iface.Address); err == nil {
		used[ifaceAddr.Addr()] = true
	}

	// reserve peer addresses
	rows, err := s.DB.QueryContext(ctx, `SELECT address FROM peers WHERE interface_id=?`, iface.ID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return "", err
		}
		if p, err := netip.ParsePrefix(addr); err == nil {
			used[p.Addr()] = true
		}
	}

	// walk candidates: skip .0 (network) and .255 (broadcast for /24 or similar)
	netAddr := prefix.Masked().Addr()
	ip := netAddr.Next() // .1
	for prefix.Contains(ip) {
		// skip broadcast (all-ones host portion) for IPv4
		if !used[ip] && !isBroadcast(ip, prefix) {
			return ip.String() + "/32", nil
		}
		ip = ip.Next()
	}
	return "", fmt.Errorf("no free addresses in %s", iface.Subnet)
}

func isBroadcast(a netip.Addr, p netip.Prefix) bool {
	if !a.Is4() {
		return false
	}
	if p.Bits() >= 31 {
		return false // /31, /32 — no broadcast concept
	}
	// last address in prefix
	b := p.Addr().As4()
	hostBits := 32 - p.Bits()
	// set host bits to 1
	for i := 0; i < 4; i++ {
		byteBits := hostBits
		if byteBits > 8 {
			byteBits = 8
		}
		mask := byte((1 << byteBits) - 1)
		b[3-i] |= mask
		hostBits -= byteBits
		if hostBits <= 0 {
			break
		}
	}
	return a.As4() == b
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}
