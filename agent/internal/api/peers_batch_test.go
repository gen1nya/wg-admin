package api_test

import (
	"testing"
)

// kernelHasPeer reports whether the mock kernel currently serves a peer with
// the given public key on the interface.
func (ts *testServer) kernelHasPeer(t *testing.T, iface, pub string) bool {
	t.Helper()
	st, err := ts.kernel.ShowInterface(iface)
	if err != nil {
		t.Fatalf("ShowInterface(%s): %v", iface, err)
	}
	for _, p := range st.Peers {
		if p.PublicKey == pub {
			return true
		}
	}
	return false
}

// TestCreatePeerRejectsBadAddress covers the address-validation guard: a
// caller must not be able to claim a non-host CIDR, an out-of-subnet address,
// the interface's own address, or an address already held by another peer.
func TestCreatePeerRejectsBadAddress(t *testing.T) {
	ts := newTestServer(t)

	cases := []struct {
		name string
		addr string
		want int
	}{
		{"catch-all", "0.0.0.0/0", 400},
		{"not-a-host-cidr", "10.8.1.0/24", 400},
		{"outside-subnet", "10.9.9.9/32", 400},
		{"interface-own-address", "10.8.1.1/32", 400},
		{"garbage", "not-an-ip", 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := ts.do(t, "POST", "/interfaces/wg0/peers",
				map[string]any{"name": "x", "address": c.addr})
			if w.Code != c.want {
				t.Errorf("addr %q: want %d, got %d (%s)", c.addr, c.want, w.Code, w.Body.String())
			}
		})
	}
}

// TestCreatePeerForeignAddressHijack: claiming another peer's /32 must be
// refused (409), not silently move the allowed-ip and cut the first client off.
func TestCreatePeerForeignAddressHijack(t *testing.T) {
	ts := newTestServer(t)

	w := ts.do(t, "POST", "/interfaces/wg0/peers",
		map[string]any{"name": "victim", "address": "10.8.1.5/32"})
	if w.Code != 201 {
		t.Fatalf("first create: %d %s", w.Code, w.Body.String())
	}
	victim := decode[map[string]any](t, w)
	victimPub, _ := victim["public_key"].(string)

	w = ts.do(t, "POST", "/interfaces/wg0/peers",
		map[string]any{"name": "attacker", "address": "10.8.1.5/32"})
	if w.Code != 409 {
		t.Fatalf("hijack attempt: want 409, got %d %s", w.Code, w.Body.String())
	}
	// Victim must still own the allowed-ip in the kernel.
	if !ts.kernelHasPeer(t, "wg0", victimPub) {
		t.Error("victim peer lost its kernel entry after hijack attempt")
	}
}

// TestRevokeReachesKernel: PATCH {enabled:false} must remove the peer from the
// kernel immediately (revoke), and {enabled:true} must restore it — not just
// flip the DB flag.
func TestRevokeReachesKernel(t *testing.T) {
	ts := newTestServer(t)

	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "revokee"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := decode[map[string]any](t, w)
	id := int(created["id"].(float64))
	pub, _ := created["public_key"].(string)

	if !ts.kernelHasPeer(t, "wg0", pub) {
		t.Fatal("peer absent from kernel right after create")
	}

	// Disable → must be gone from the kernel.
	w = ts.do(t, "PATCH", httpPath("/peers/%d", id), map[string]any{"enabled": false})
	if w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if ts.kernelHasPeer(t, "wg0", pub) {
		t.Error("disabled (revoked) peer still served by kernel")
	}

	// Re-enable → must be back.
	w = ts.do(t, "PATCH", httpPath("/peers/%d", id), map[string]any{"enabled": true})
	if w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	if !ts.kernelHasPeer(t, "wg0", pub) {
		t.Error("re-enabled peer not restored to kernel")
	}
}

// TestUpdatePeerNonKernelFieldsSkipKernel: a name/notes patch must not touch
// the kernel peer set (no spurious add/remove).
func TestUpdatePeerMetadataDoesNotTouchKernel(t *testing.T) {
	ts := newTestServer(t)
	w := ts.do(t, "POST", "/interfaces/wg0/peers", map[string]any{"name": "n"})
	created := decode[map[string]any](t, w)
	id := int(created["id"].(float64))
	pub, _ := created["public_key"].(string)

	w = ts.do(t, "PATCH", httpPath("/peers/%d", id), map[string]any{"notes": "renamed"})
	if w.Code != 200 {
		t.Fatalf("patch notes: %d %s", w.Code, w.Body.String())
	}
	if !ts.kernelHasPeer(t, "wg0", pub) {
		t.Error("metadata patch dropped the peer from the kernel")
	}
}
