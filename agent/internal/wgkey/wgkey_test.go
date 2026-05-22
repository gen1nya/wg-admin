package wgkey

import (
	"encoding/base64"
	"testing"
)

func TestGenPairRoundtrip(t *testing.T) {
	priv, pub, err := GenPair()
	if err != nil {
		t.Fatalf("GenPair: %v", err)
	}
	if priv == "" || pub == "" {
		t.Fatal("empty keys")
	}
	// Both should be 44-char base64 of 32-byte values.
	if len(priv) != 44 || len(pub) != 44 {
		t.Errorf("unexpected key length: priv=%d pub=%d", len(priv), len(pub))
	}
	// Derivation must match GenPair's pub.
	derived, err := PublicFromPrivate(priv)
	if err != nil {
		t.Fatalf("PublicFromPrivate: %v", err)
	}
	if derived != pub {
		t.Errorf("derived pub mismatch:\n  got  %s\n  want %s", derived, pub)
	}
}

func TestGenPairUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		priv, _, err := GenPair()
		if err != nil {
			t.Fatalf("GenPair: %v", err)
		}
		if seen[priv] {
			t.Fatalf("duplicate private key at iter %d", i)
		}
		seen[priv] = true
	}
}

func TestPublicFromPrivateInvalid(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"not base64", "not!base64!"},
		{"wrong length", base64.StdEncoding.EncodeToString([]byte("too short"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PublicFromPrivate(tc.in); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
