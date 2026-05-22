package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/gen1nya/wg-admin/agent/internal/model"
)

var ErrNotFound = errors.New("not found")

func scanInterface(row interface{ Scan(...any) error }) (model.Interface, error) {
	var i model.Interface
	var mtu, defaultExitID sql.NullInt64
	err := row.Scan(
		&i.ID, &i.Name, &i.Address, &i.Subnet, &i.ListenPort,
		&mtu, &i.PrivateKey, &i.PublicEndpoint, &i.PublicPort,
		&i.DNS, &i.Keepalive, &defaultExitID, &i.ClientAllowedIPs,
		&i.CustomPostUp, &i.CustomPostDown, &i.Enabled, &i.Role, &i.CreatedAt,
	)
	if err != nil {
		return i, err
	}
	if mtu.Valid {
		v := int(mtu.Int64)
		i.MTU = &v
	}
	if defaultExitID.Valid {
		i.DefaultExitID = &defaultExitID.Int64
	}
	return i, nil
}

const interfaceCols = `id, name, address, subnet, listen_port, mtu,
	private_key, public_endpoint, public_port, dns, keepalive,
	default_exit_id, client_allowed_ips, custom_postup, custom_postdown, enabled, role, created_at`

func (s *Store) ListInterfaces(ctx context.Context) ([]model.Interface, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+interfaceCols+` FROM interfaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Interface
	for rows.Next() {
		i, err := scanInterface(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) GetInterface(ctx context.Context, id int64) (model.Interface, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+interfaceCols+` FROM interfaces WHERE id=?`, id)
	i, err := scanInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

func (s *Store) GetInterfaceByName(ctx context.Context, name string) (model.Interface, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+interfaceCols+` FROM interfaces WHERE name=?`, name)
	i, err := scanInterface(row)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	return i, err
}

// UpsertInterface inserts or updates an interface record by name.
// Returns the row's id.
func (s *Store) UpsertInterface(ctx context.Context, i *model.Interface) (int64, error) {
	var mtu any
	if i.MTU != nil {
		mtu = *i.MTU
	}
	var defaultExitID any
	if i.DefaultExitID != nil {
		defaultExitID = *i.DefaultExitID
	}
	clientAllowed := i.ClientAllowedIPs
	if clientAllowed == "" {
		clientAllowed = "0.0.0.0/0"
	}
	role := i.Role
	if role == "" {
		role = model.RoleClients
	}
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO interfaces
		  (name, address, subnet, listen_port, mtu, private_key,
		   public_endpoint, public_port, dns, keepalive, default_exit_id,
		   client_allowed_ips, custom_postup, custom_postdown, enabled, role, created_at)
		VALUES (?,?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
		  address=excluded.address, subnet=excluded.subnet,
		  listen_port=excluded.listen_port, mtu=excluded.mtu,
		  private_key=excluded.private_key,
		  public_endpoint=excluded.public_endpoint, public_port=excluded.public_port,
		  dns=excluded.dns, keepalive=excluded.keepalive,
		  default_exit_id=excluded.default_exit_id,
		  client_allowed_ips=excluded.client_allowed_ips,
		  custom_postup=excluded.custom_postup, custom_postdown=excluded.custom_postdown,
		  enabled=excluded.enabled, role=excluded.role`,
		i.Name, i.Address, i.Subnet, i.ListenPort, mtu, i.PrivateKey,
		i.PublicEndpoint, i.PublicPort, i.DNS, i.Keepalive, defaultExitID,
		clientAllowed, i.CustomPostUp, i.CustomPostDown, i.Enabled, role, i.CreatedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("upsert interface: %w", err)
	}
	// Lastly, read the id (RETURNING needs a different path in modernc.org/sqlite)
	row := s.DB.QueryRowContext(ctx, `SELECT id FROM interfaces WHERE name=?`, i.Name)
	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, err
	}
	_ = res
	return id, nil
}
