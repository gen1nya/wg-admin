package kernel

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Real runs WireGuard/ipset/ip commands on the host.
//
// Binary paths default to bare names resolved via $PATH. For systemd units
// set them explicitly (e.g. WgPath: "/usr/bin/wg") so a trimmed PATH doesn't
// surprise you.
//
// For dev-without-root, set SudoPrefix (e.g. []string{"sudo"}) and add
// NOPASSWD rules for /usr/bin/wg, /sbin/ip, /usr/sbin/ipset in sudoers.
//
// Netns runs every command inside `ip netns exec <Netns> ...` — used only by
// integration tests.
type Real struct {
	WgPath    string
	IPPath    string
	IPSetPath string
	NFTPath   string

	SudoPrefix []string
	Timeout    time.Duration
	Netns      string
}

func NewReal() *Real {
	return &Real{
		WgPath:    "wg",
		IPPath:    "ip",
		IPSetPath: "ipset",
		NFTPath:   "nft",
		Timeout:   5 * time.Second,
	}
}

func (r *Real) Version() string { return "real" }

func (r *Real) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return 5 * time.Second
}

// wrapArgv builds the final argv after optional sudo and netns wrapping.
func (r *Real) wrapArgv(bin string, args []string) (string, []string) {
	argv := append([]string{bin}, args...)
	if r.Netns != "" {
		argv = append([]string{r.IPPath, "netns", "exec", r.Netns}, argv...)
	}
	if len(r.SudoPrefix) > 0 {
		argv = append(append([]string{}, r.SudoPrefix...), argv...)
	}
	return argv[0], argv[1:]
}

func (r *Real) run(bin string, args ...string) ([]byte, error) {
	return r.runStdin(bin, nil, args...)
}

func (r *Real) runStdin(bin string, stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout())
	defer cancel()
	first, rest := r.wrapArgv(bin, args)
	cmd := exec.CommandContext(ctx, first, rest...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		slog.Debug("kernel exec failed", "bin", first, "args", rest, "err", err, "stderr", stderr.String())
		return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", first, strings.Join(rest, " "), err, strings.TrimSpace(stderr.String()))
	}
	slog.Debug("kernel exec ok", "bin", first, "args", rest, "bytes", stdout.Len())
	return stdout.Bytes(), nil
}

// ListInterfaces returns every WG interface visible to the kernel.
func (r *Real) ListInterfaces() ([]string, error) {
	out, err := r.run(r.WgPath, "show", "interfaces")
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return []string{}, nil
	}
	return strings.Fields(s), nil
}

// ShowInterface parses `wg show <name> dump`.
func (r *Real) ShowInterface(name string) (InterfaceStatus, error) {
	if err := validateInterfaceName(name); err != nil {
		return InterfaceStatus{}, err
	}
	out, err := r.run(r.WgPath, "show", name, "dump")
	if err != nil {
		if isWGMissing(err) {
			return InterfaceStatus{}, fmt.Errorf("%w: %s", ErrInterfaceNotFound, name)
		}
		return InterfaceStatus{}, err
	}
	return parseWGDump(name, out)
}

// SetPeer upserts allowed-ips on the given peer. Idempotent.
func (r *Real) SetPeer(iface, publicKey, allowedIPs string) error {
	if err := validateInterfaceName(iface); err != nil {
		return err
	}
	if err := validatePublicKey(publicKey); err != nil {
		return err
	}
	if err := validateAllowedIPs(allowedIPs); err != nil {
		return err
	}
	_, err := r.run(r.WgPath, "set", iface, "peer", publicKey, "allowed-ips", allowedIPs)
	if err != nil && isWGMissing(err) {
		return fmt.Errorf("%w: %s", ErrInterfaceNotFound, iface)
	}
	return err
}

// RemovePeer removes a peer. Non-existent peer is not an error (wg returns 0).
func (r *Real) RemovePeer(iface, publicKey string) error {
	if err := validateInterfaceName(iface); err != nil {
		return err
	}
	if err := validatePublicKey(publicKey); err != nil {
		return err
	}
	_, err := r.run(r.WgPath, "set", iface, "peer", publicKey, "remove")
	if err != nil && isWGMissing(err) {
		return fmt.Errorf("%w: %s", ErrInterfaceNotFound, iface)
	}
	return err
}

// IPSetList returns entries of a hash:net set as CIDR strings.
func (r *Real) IPSetList(name string) ([]string, error) {
	if err := validateIPSetName(name); err != nil {
		return nil, err
	}
	out, err := r.run(r.IPSetPath, "list", name, "-output", "save")
	if err != nil {
		if isIPSetMissing(err) {
			return nil, fmt.Errorf("%w: %s", ErrIPSetNotFound, name)
		}
		return nil, err
	}
	var entries []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "add ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			entries = append(entries, fields[2])
		}
	}
	return entries, nil
}

// IPSetDestroy removes a set. Missing set is not an error (idempotent).
func (r *Real) IPSetDestroy(name string) error {
	if err := validateIPSetName(name); err != nil {
		return err
	}
	_, err := r.run(r.IPSetPath, "destroy", name)
	if err != nil && isIPSetMissing(err) {
		return nil
	}
	return err
}

// IPSetReplace atomically replaces the contents of a hash:net set. If the set
// doesn't exist, it's created first.
func (r *Real) IPSetReplace(name string, entries []string) error {
	if err := validateIPSetName(name); err != nil {
		return err
	}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, err := netip.ParsePrefix(e); err != nil {
			return fmt.Errorf("ipset %s entry %q: %w", name, e, err)
		}
	}

	if _, err := r.run(r.IPSetPath, "create", name, "hash:net", "-exist"); err != nil {
		return err
	}

	tmp := name + "_new"
	// stale tmp from a previously crashed run
	_, _ = r.run(r.IPSetPath, "destroy", tmp)

	var b strings.Builder
	fmt.Fprintf(&b, "create %s hash:net\n", tmp)
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		fmt.Fprintf(&b, "add %s %s\n", tmp, e)
	}
	if _, err := r.runStdin(r.IPSetPath, []byte(b.String()), "restore", "-exist"); err != nil {
		_, _ = r.run(r.IPSetPath, "destroy", tmp)
		return err
	}
	if _, err := r.run(r.IPSetPath, "swap", name, tmp); err != nil {
		_, _ = r.run(r.IPSetPath, "destroy", tmp)
		return err
	}
	if _, err := r.run(r.IPSetPath, "destroy", tmp); err != nil {
		return err
	}
	return nil
}

// --- routes ---

// RouteList returns routes from the given table.
// `ip route show table <table>` — missing table yields empty, not an error.
func (r *Real) RouteList(table string) ([]RouteEntry, error) {
	if err := validateTableName(table); err != nil {
		return nil, err
	}
	out, err := r.run(r.IPPath, "route", "show", "table", table)
	if err != nil {
		// "ip route show table foo" on empty/missing table prints nothing + exit 0
		// on most kernels. But very old iproute2 may error. Treat as empty.
		if isRouteTableEmpty(err) {
			return []RouteEntry{}, nil
		}
		return nil, err
	}
	return parseRouteList(table, out), nil
}

// RouteReplace upserts a route: `ip route replace <dest> dev <dev> [via <via>] table <table>`.
func (r *Real) RouteReplace(rt RouteEntry) error {
	if err := validateTableName(rt.Table); err != nil {
		return err
	}
	if err := validateRouteDest(rt.Dest); err != nil {
		return err
	}
	args := []string{"route", "replace", rt.Dest}
	if rt.Dev != "" {
		if err := validateInterfaceName(rt.Dev); err != nil {
			return err
		}
		args = append(args, "dev", rt.Dev)
	}
	if rt.Via != "" {
		if err := validateIPAddress(rt.Via); err != nil {
			return err
		}
		args = append(args, "via", rt.Via)
	}
	args = append(args, "table", rt.Table)
	_, err := r.run(r.IPPath, args...)
	return err
}

// RouteDelete removes a route. Missing route is not an error.
func (r *Real) RouteDelete(table, dest string) error {
	if err := validateTableName(table); err != nil {
		return err
	}
	if err := validateRouteDest(dest); err != nil {
		return err
	}
	_, err := r.run(r.IPPath, "route", "del", dest, "table", table)
	if err != nil && isRouteMissing(err) {
		return nil
	}
	return err
}

// --- rules ---

// RuleList returns all fwmark-based rules. Rules of other forms are skipped.
func (r *Real) RuleList() ([]RuleEntry, error) {
	out, err := r.run(r.IPPath, "rule", "show")
	if err != nil {
		return nil, err
	}
	return parseRuleList(out), nil
}

// RuleAdd: `ip rule add priority N fwmark M table T`.
func (r *Real) RuleAdd(rule RuleEntry) error {
	if err := validateTableName(rule.Table); err != nil {
		return err
	}
	if rule.Priority <= 0 {
		return fmt.Errorf("rule priority must be > 0, got %d", rule.Priority)
	}
	_, err := r.run(r.IPPath, "rule", "add",
		"priority", strconv.Itoa(rule.Priority),
		"fwmark", fmt.Sprintf("0x%x", rule.Fwmark),
		"table", rule.Table,
	)
	return err
}

// RuleDel by priority. Idempotent.
func (r *Real) RuleDel(priority int) error {
	_, err := r.run(r.IPPath, "rule", "del", "priority", strconv.Itoa(priority))
	if err != nil && isRuleMissing(err) {
		return nil
	}
	return err
}

// --- nftables ---

// NFTList returns raw `nft list table inet <table>` output. Missing table = empty string, no error.
func (r *Real) NFTList(table string) (string, error) {
	if err := validateNFTTableName(table); err != nil {
		return "", err
	}
	out, err := r.run(r.NFTPath, "list", "table", "inet", table)
	if err != nil {
		if isNFTMissing(err) {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}

// NFTApply pipes the given ruleset into `nft -f -`. nft treats the whole
// file as a single atomic transaction — partial application doesn't happen.
func (r *Real) NFTApply(ruleset string) error {
	_, err := r.runStdin(r.NFTPath, []byte(ruleset), "-f", "-")
	return err
}

// --- parsing helpers ---

// parseWGDump parses the tab-separated output of `wg show <name> dump`.
// First line: <privkey>\t<pubkey>\t<listen_port>\t<fwmark>
// Each subsequent line: <pub>\t<psk>\t<endpoint>\t<allowed_ips>\t<handshake>\t<rx>\t<tx>\t<keepalive>
func parseWGDump(name string, out []byte) (InterfaceStatus, error) {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return InterfaceStatus{}, fmt.Errorf("empty dump for %s", name)
	}
	head := strings.Split(lines[0], "\t")
	if len(head) < 4 {
		return InterfaceStatus{}, fmt.Errorf("malformed interface line: %q", lines[0])
	}
	port, _ := strconv.Atoi(head[2])
	st := InterfaceStatus{
		Name:       name,
		PublicKey:  head[1],
		ListenPort: port,
		FwMark:     parseFwmark(head[3]),
	}
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 8 {
			continue
		}
		hs, _ := strconv.ParseInt(f[4], 10, 64)
		rx, _ := strconv.ParseInt(f[5], 10, 64)
		tx, _ := strconv.ParseInt(f[6], 10, 64)
		ep := f[2]
		if ep == "(none)" {
			ep = ""
		}
		allowed := f[3]
		if allowed == "(none)" {
			allowed = ""
		}
		st.Peers = append(st.Peers, PeerStatus{
			PublicKey:       f[0],
			Endpoint:        ep,
			AllowedIPs:      allowed,
			LatestHandshake: hs,
			RxBytes:         rx,
			TxBytes:         tx,
		})
	}
	return st, nil
}

func parseFwmark(s string) int {
	s = strings.TrimSpace(s)
	if s == "" || s == "off" {
		return 0
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, _ := strconv.ParseInt(s[2:], 16, 64)
		return int(v)
	}
	v, _ := strconv.Atoi(s)
	return v
}

func isWGMissing(err error) bool {
	s := err.Error()
	return strings.Contains(s, "Unable to access interface") ||
		strings.Contains(s, "No such device") ||
		strings.Contains(s, "No such file or directory")
}

func isIPSetMissing(err error) bool {
	s := err.Error()
	return strings.Contains(s, "set with the given name does not exist") ||
		strings.Contains(s, "The set with the given name does not exist")
}

// parseRouteList parses `ip route show table <t>` output.
// Each line: "<dest> [via <gw>] [dev <dev>] [proto ...] [scope ...] [src ...] [metric ...]"
// We care only about dest/via/dev; other tokens are ignored.
func parseRouteList(table string, out []byte) []RouteEntry {
	var routes []RouteEntry
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		r := RouteEntry{Table: table, Dest: fields[0]}
		for i := 1; i < len(fields)-1; i++ {
			switch fields[i] {
			case "via":
				r.Via = fields[i+1]
			case "dev":
				r.Dev = fields[i+1]
			}
		}
		routes = append(routes, r)
	}
	return routes
}

// parseRuleList parses `ip rule show`. Only rows with fwmark + lookup are
// returned; other forms (from/iif/oif/suppress) are ignored.
// Line format: "<prio>:\tfrom all fwmark <hex> lookup <table>"
func parseRuleList(out []byte) []RuleEntry {
	var rules []RuleEntry
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		prio, err := strconv.Atoi(strings.TrimSpace(line[:colon]))
		if err != nil {
			continue
		}
		rest := line[colon+1:]
		// Must contain both "fwmark" and "lookup".
		if !strings.Contains(rest, "fwmark") || !strings.Contains(rest, "lookup") {
			continue
		}
		fields := strings.Fields(rest)
		var fwmark uint32
		var table string
		for i := 0; i < len(fields)-1; i++ {
			switch fields[i] {
			case "fwmark":
				fwmark = parseFwmarkU32(fields[i+1])
			case "lookup":
				table = fields[i+1]
			}
		}
		if table == "" {
			continue
		}
		rules = append(rules, RuleEntry{Priority: prio, Fwmark: fwmark, Table: table})
	}
	return rules
}

func parseFwmarkU32(s string) uint32 {
	// Can be "0x1", "1", or "0x1/0xff" (with mask — we ignore mask for now).
	if i := strings.Index(s, "/"); i > 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		v, _ := strconv.ParseUint(s[2:], 16, 32)
		return uint32(v)
	}
	v, _ := strconv.ParseUint(s, 10, 32)
	return uint32(v)
}

func isRouteMissing(err error) bool {
	s := err.Error()
	return strings.Contains(s, "No such process") ||
		strings.Contains(s, "RTNETLINK answers: No such process")
}

func isRouteTableEmpty(err error) bool {
	// Some kernels / iproute2 versions: empty table → exit 0 (no error).
	// Newer may emit nothing or "Error: ipv4: FIB table does not exist."
	s := err.Error()
	return strings.Contains(s, "FIB table does not exist")
}

func isRuleMissing(err error) bool {
	s := err.Error()
	return strings.Contains(s, "RTNETLINK answers: No such file or directory") ||
		strings.Contains(s, "No such file or directory")
}

func isNFTMissing(err error) bool {
	s := err.Error()
	return strings.Contains(s, "No such file or directory") ||
		strings.Contains(s, "does not exist")
}
