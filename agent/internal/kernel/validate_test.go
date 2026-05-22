package kernel

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateInterfaceName(t *testing.T) {
	ok := []string{"wg0", "wg-exit-b", "wg_test-a", "x.y.z", "a"}
	for _, n := range ok {
		if err := validateInterfaceName(n); err != nil {
			t.Errorf("validateInterfaceName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "wg 0", "wg/0", "x" + strings.Repeat("y", 15), "имя"}
	for _, n := range bad {
		if err := validateInterfaceName(n); !errors.Is(err, ErrInvalidName) {
			t.Errorf("validateInterfaceName(%q) = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestValidateIPSetName(t *testing.T) {
	ok := []string{"direct", "telegram-dc", "set_1"}
	for _, n := range ok {
		if err := validateIPSetName(n); err != nil {
			t.Errorf("validateIPSetName(%q) = %v", n, err)
		}
	}
	bad := []string{"", "set.name", "a" + strings.Repeat("b", 31)}
	for _, n := range bad {
		if err := validateIPSetName(n); !errors.Is(err, ErrInvalidName) {
			t.Errorf("validateIPSetName(%q) = %v, want ErrInvalidName", n, err)
		}
	}
}

func TestValidatePublicKey(t *testing.T) {
	// 32 zero bytes -> "AAAA...AAAA=" (44 chars)
	valid := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	if err := validatePublicKey(valid); err != nil {
		t.Errorf("validatePublicKey(valid) = %v", err)
	}
	bad := []string{
		"",                                    // empty
		"short",                               // wrong length
		strings.Repeat("x", 44),               // wrong padding/chars for valid base64
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", // too short
	}
	for _, k := range bad {
		if err := validatePublicKey(k); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("validatePublicKey(%q) = %v, want ErrInvalidKey", k, err)
		}
	}
}

func TestValidateAllowedIPs(t *testing.T) {
	if err := validateAllowedIPs("10.0.0.0/24"); err != nil {
		t.Errorf("single prefix: %v", err)
	}
	if err := validateAllowedIPs("10.0.0.0/24, 192.168.1.0/24"); err != nil {
		t.Errorf("multi prefix: %v", err)
	}
	if err := validateAllowedIPs(""); err == nil {
		t.Error("empty must error")
	}
	if err := validateAllowedIPs("not-a-prefix"); err == nil {
		t.Error("garbage must error")
	}
}
