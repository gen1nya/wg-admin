// Package wgkey generates WireGuard curve25519 keypairs.
package wgkey

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// GenPair returns (privateBase64, publicBase64).
func GenPair() (string, string, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return "", "", fmt.Errorf("rand: %w", err)
	}
	// curve25519 clamping
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("derive pubkey: %w", err)
	}
	return base64.StdEncoding.EncodeToString(priv[:]),
		base64.StdEncoding.EncodeToString(pub), nil
}

// GenPSK returns a fresh WireGuard preshared key: 32 random bytes, base64.
// Unlike a keypair there's no clamping — a PSK is opaque symmetric key material.
func GenPSK() (string, error) {
	var psk [32]byte
	if _, err := rand.Read(psk[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return base64.StdEncoding.EncodeToString(psk[:]), nil
}

// PublicFromPrivate derives the public key (base64) from a base64-encoded
// 32-byte WireGuard private key.
func PublicFromPrivate(privB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("private key must be 32 bytes, got %d", len(raw))
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("derive pubkey: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}
