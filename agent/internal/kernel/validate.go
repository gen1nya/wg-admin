package kernel

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

var (
	ifaceNameRE = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	ipsetNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

func validateInterfaceName(name string) error {
	if name == "" || len(name) > 15 || !ifaceNameRE.MatchString(name) {
		return fmt.Errorf("%w: interface %q", ErrInvalidName, name)
	}
	return nil
}

func validatePublicKey(k string) error {
	if len(k) != 44 {
		return fmt.Errorf("%w: expected 44 base64 chars, got %d", ErrInvalidKey, len(k))
	}
	decoded, err := base64.StdEncoding.DecodeString(k)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("%w: decoded %d bytes, want 32", ErrInvalidKey, len(decoded))
	}
	return nil
}

// validatePresharedKey accepts the same shape as a WG key: 44 base64 chars
// decoding to 32 bytes. Callers pass "" to mean "no PSK" and must skip this.
func validatePresharedKey(k string) error {
	if len(k) != 44 {
		return fmt.Errorf("%w: preshared key must be 44 base64 chars, got %d", ErrInvalidKey, len(k))
	}
	decoded, err := base64.StdEncoding.DecodeString(k)
	if err != nil {
		return fmt.Errorf("%w: preshared key: %v", ErrInvalidKey, err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("%w: preshared key decoded %d bytes, want 32", ErrInvalidKey, len(decoded))
	}
	return nil
}

func validateAllowedIPs(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("allowed-ips is empty")
	}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, err := netip.ParsePrefix(p); err != nil {
			return fmt.Errorf("invalid allowed-ip %q: %w", p, err)
		}
	}
	return nil
}

func validateIPSetName(name string) error {
	if name == "" || len(name) > 31 || !ipsetNameRE.MatchString(name) {
		return fmt.Errorf("%w: ipset %q", ErrInvalidName, name)
	}
	return nil
}

var (
	tableNameRE    = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`) // name or numeric id
	nftTableNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

func validateTableName(name string) error {
	if name == "" || len(name) > 64 || !tableNameRE.MatchString(name) {
		return fmt.Errorf("%w: table %q", ErrInvalidName, name)
	}
	return nil
}

func validateNFTTableName(name string) error {
	if name == "" || len(name) > 64 || !nftTableNameRE.MatchString(name) {
		return fmt.Errorf("%w: nft table %q", ErrInvalidName, name)
	}
	return nil
}

func validateRouteDest(dest string) error {
	if dest == "default" {
		return nil
	}
	if _, err := netip.ParsePrefix(dest); err != nil {
		// Accept a bare IP as /32 or /128 convenience — but ip route wants
		// the explicit form, so reject and tell the caller.
		if _, err2 := netip.ParseAddr(dest); err2 == nil {
			return fmt.Errorf("route dest %q: use CIDR (e.g. %s/32) or 'default'", dest, dest)
		}
		return fmt.Errorf("route dest %q: %w", dest, err)
	}
	return nil
}

func validateIPAddress(addr string) error {
	if _, err := netip.ParseAddr(addr); err != nil {
		return fmt.Errorf("invalid IP %q: %w", addr, err)
	}
	return nil
}
