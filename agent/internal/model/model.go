package model

// Interface roles.
//
//	RoleClients — accepts end-user peers; API can CRUD peers, render .conf, auto-allocate /32.
//	RoleMesh    — tunnel to another server (exit, mesh link); peers are remote endpoints,
//	              not end-users. Read-only via API; tier-2 exits table governs routing.
const (
	RoleClients = "clients"
	RoleMesh    = "mesh"
)

type Mark struct {
	ID           int64  `json:"id"`
	Fwmark       int    `json:"fwmark"`
	Name         string `json:"name"`
	RoutingTable string `json:"routing_table"`
	Description  string `json:"description"`
}

type Exit struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"` // direct|wg|xray|custom
	OutInterface string `json:"out_interface"`
	MarkID       int64  `json:"mark_id"`
	Masquerade   bool   `json:"masquerade"`
	Description  string `json:"description"`
	Enabled      bool   `json:"enabled"`
}

type Interface struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Address          string `json:"address"`
	Subnet           string `json:"subnet"`
	ListenPort       int    `json:"listen_port"`
	MTU              *int   `json:"mtu,omitempty"`
	PrivateKey       string `json:"private_key"`
	PublicEndpoint   string `json:"public_endpoint"`
	PublicPort       int    `json:"public_port"`
	DNS              string `json:"dns"`
	Keepalive        int    `json:"keepalive"`
	DefaultExitID    *int64 `json:"default_exit_id,omitempty"`
	ClientAllowedIPs string `json:"client_allowed_ips"`
	CustomPostUp     string `json:"custom_postup"`
	CustomPostDown   string `json:"custom_postdown"`
	Enabled          bool   `json:"enabled"`
	Role             string `json:"role"` // clients|mesh
	CreatedAt        int64  `json:"created_at"`
}

type Peer struct {
	ID          int64  `json:"id"`
	InterfaceID int64  `json:"interface_id"`
	Name        string `json:"name"`
	PublicKey   string `json:"public_key"`
	PrivateKey  string `json:"private_key"`
	// PresharedKey never leaves the agent in API JSON (json:"-") — it's a
	// secret used only to render the client .conf and configure the kernel.
	// Empty means the peer has no PSK.
	PresharedKey  string `json:"-"`
	Address       string `json:"address"`
	DefaultExitID *int64 `json:"default_exit_id,omitempty"`
	Enabled       bool   `json:"enabled"`
	Notes         string `json:"notes"`
	Tags          string `json:"tags"` // JSON-encoded array
	CreatedAt     int64  `json:"created_at"`
}

type IPSet struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type IPSetEntry struct {
	ID      int64  `json:"id"`
	IPSetID int64  `json:"ipset_id"`
	CIDR    string `json:"cidr"`
	Comment string `json:"comment"`
}

type RoutingRule struct {
	ID         int64  `json:"id"`
	Scope      string `json:"scope"` // global|interface|peer
	ScopeID    *int64 `json:"scope_id,omitempty"`
	MatchType  string `json:"match_type"` // cidr|ipset|domain|all
	MatchValue string `json:"match_value"`
	ExitID     int64  `json:"exit_id"`
	Priority   int    `json:"priority"`
	Enabled    bool   `json:"enabled"`
}

// Plan states.
const (
	PlanPending   = "pending"
	PlanApplied   = "applied"
	PlanConfirmed = "confirmed"
	PlanReverted  = "reverted"
	PlanExpired   = "expired"
)

type Plan struct {
	ID          int64  `json:"id"`
	CreatedAt   int64  `json:"created_at"`
	CreatedBy   string `json:"created_by"`
	Description string `json:"description"`
	Desired     string `json:"desired"` // JSON DesiredState (target)
	Diff        string `json:"diff"`    // JSON summary for UI
	State       string `json:"state"`
	AppliedAt   *int64 `json:"applied_at,omitempty"`
	ConfirmedAt *int64 `json:"confirmed_at,omitempty"`
	RevertedAt  *int64 `json:"reverted_at,omitempty"`
	TimeoutSec  int    `json:"timeout_sec"`
	SnapshotPre string `json:"snapshot_pre,omitempty"`
}

type AuditEntry struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	EntityType string `json:"entity_type"`
	EntityID   *int64 `json:"entity_id,omitempty"`
	Payload    string `json:"payload"`
}
