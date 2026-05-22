package kernel

import "testing"

func TestParseFwmark(t *testing.T) {
	cases := map[string]int{
		"":     0,
		"off":  0,
		"0":    0,
		"0x1":  1,
		"0xff": 255,
		"42":   42,
	}
	for in, want := range cases {
		if got := parseFwmark(in); got != want {
			t.Errorf("parseFwmark(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseWGDump(t *testing.T) {
	// Tab-separated; first line = interface, rest = peers.
	// Mirrors `wg show wg0 dump` format.
	priv := "cHJpdmF0ZV9rZXlfMzJieXRlc19oZXJlX3RvX3NhdGlzZnk="
	pub := "cHViX2tleV8zMmJ5dGVzX2hlcmVfdG9fc2F0aXNmeV9hcGk="
	peerPub := "cGVlcl9wdWJfMzJieXRlc19oZXJlX3RvX3NhdGlzZnlfaw=="
	dump := priv + "\t" + pub + "\t51820\t0x1\n" +
		peerPub + "\t(none)\t203.0.113.10:51820\t10.8.1.2/32\t1700000000\t12345\t6789\t25\n" +
		"\n"

	st, err := parseWGDump("wg0", []byte(dump))
	if err != nil {
		t.Fatalf("parseWGDump: %v", err)
	}
	if st.Name != "wg0" || st.PublicKey != pub || st.ListenPort != 51820 || st.FwMark != 1 {
		t.Errorf("interface: %+v", st)
	}
	if len(st.Peers) != 1 {
		t.Fatalf("peers=%d, want 1", len(st.Peers))
	}
	p := st.Peers[0]
	if p.PublicKey != peerPub {
		t.Errorf("peer pub=%q", p.PublicKey)
	}
	if p.Endpoint != "203.0.113.10:51820" {
		t.Errorf("endpoint=%q", p.Endpoint)
	}
	if p.AllowedIPs != "10.8.1.2/32" {
		t.Errorf("allowed=%q", p.AllowedIPs)
	}
	if p.LatestHandshake != 1700000000 || p.RxBytes != 12345 || p.TxBytes != 6789 {
		t.Errorf("counters: %+v", p)
	}
}

func TestParseWGDumpEmptyPeers(t *testing.T) {
	pub := "cHViX2tleV8zMmJ5dGVzX2hlcmVfdG9fc2F0aXNmeV9hcGk="
	dump := "privkey\t" + pub + "\t51820\toff\n"
	st, err := parseWGDump("wg-exit-b", []byte(dump))
	if err != nil {
		t.Fatalf("parseWGDump: %v", err)
	}
	if st.FwMark != 0 {
		t.Errorf("fwmark=%d, want 0", st.FwMark)
	}
	if len(st.Peers) != 0 {
		t.Errorf("peers=%d, want 0", len(st.Peers))
	}
}

func TestParseWGDumpEndpointNone(t *testing.T) {
	pub := "cHViX2tleV8zMmJ5dGVzX2hlcmVfdG9fc2F0aXNmeV9hcGk="
	peerPub := "cGVlcl9wdWJfMzJieXRlc19oZXJlX3RvX3NhdGlzZnlfaw=="
	dump := "privkey\t" + pub + "\t51820\t0\n" +
		peerPub + "\t(none)\t(none)\t(none)\t0\t0\t0\t0\n"
	st, err := parseWGDump("wg0", []byte(dump))
	if err != nil {
		t.Fatalf("parseWGDump: %v", err)
	}
	if len(st.Peers) != 1 {
		t.Fatalf("peers=%d", len(st.Peers))
	}
	if st.Peers[0].Endpoint != "" || st.Peers[0].AllowedIPs != "" {
		t.Errorf("(none) not stripped: %+v", st.Peers[0])
	}
}

func TestParseRouteList(t *testing.T) {
	out := []byte(`default via 10.99.0.1 dev wg-exit-a
10.8.1.0/24 dev wg0 proto kernel scope link src 10.8.1.1
192.168.99.0/24 dev eth0 proto static metric 1024
`)
	routes := parseRouteList("wg_tunnel", out)
	if len(routes) != 3 {
		t.Fatalf("routes=%d, want 3", len(routes))
	}
	if routes[0].Table != "wg_tunnel" || routes[0].Dest != "default" || routes[0].Via != "10.99.0.1" || routes[0].Dev != "wg-exit-a" {
		t.Errorf("route[0]=%+v", routes[0])
	}
	if routes[1].Dest != "10.8.1.0/24" || routes[1].Dev != "wg0" || routes[1].Via != "" {
		t.Errorf("route[1]=%+v", routes[1])
	}
}

func TestParseRuleList(t *testing.T) {
	// Mix of the "fwmark+lookup" form we care about and noise we must skip.
	out := []byte(`0:	from all lookup local
10000:	from all fwmark 0x1 lookup wg_tunnel
10001:	from all fwmark 0x2/0xff lookup 100
32766:	from all lookup main
32767:	from all lookup default
`)
	rules := parseRuleList(out)
	if len(rules) != 2 {
		t.Fatalf("rules=%d, want 2 (only fwmark rows)", len(rules))
	}
	if rules[0].Priority != 10000 || rules[0].Fwmark != 1 || rules[0].Table != "wg_tunnel" {
		t.Errorf("rules[0]=%+v", rules[0])
	}
	if rules[1].Priority != 10001 || rules[1].Fwmark != 2 || rules[1].Table != "100" {
		t.Errorf("rules[1]=%+v (expected mask stripped)", rules[1])
	}
}

func TestValidateTableName(t *testing.T) {
	ok := []string{"wg_tunnel", "100", "main", "some-table"}
	for _, n := range ok {
		if err := validateTableName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	bad := []string{"", "has space", "with/slash", "x\x00y"}
	for _, n := range bad {
		if err := validateTableName(n); err == nil {
			t.Errorf("%q: want error", n)
		}
	}
}

func TestValidateRouteDest(t *testing.T) {
	ok := []string{"default", "0.0.0.0/0", "10.8.1.0/24", "1.2.3.4/32"}
	for _, d := range ok {
		if err := validateRouteDest(d); err != nil {
			t.Errorf("%q: %v", d, err)
		}
	}
	bad := []string{"", "not-a-prefix", "10.0.0.0"} // bare IP rejected with hint
	for _, d := range bad {
		if err := validateRouteDest(d); err == nil {
			t.Errorf("%q: want error", d)
		}
	}
}

func TestWrapArgv(t *testing.T) {
	r := &Real{IPPath: "ip"}
	first, rest := r.wrapArgv("wg", []string{"show", "wg0"})
	if first != "wg" || len(rest) != 2 {
		t.Errorf("plain: %q %v", first, rest)
	}

	r.Netns = "wg-test"
	first, rest = r.wrapArgv("wg", []string{"show", "wg0"})
	if first != "ip" || rest[0] != "netns" || rest[1] != "exec" || rest[2] != "wg-test" || rest[3] != "wg" {
		t.Errorf("netns: %q %v", first, rest)
	}

	r.SudoPrefix = []string{"sudo", "-n"}
	first, rest = r.wrapArgv("wg", []string{"show"})
	if first != "sudo" || rest[0] != "-n" || rest[1] != "ip" || rest[2] != "netns" {
		t.Errorf("sudo+netns: %q %v", first, rest)
	}
}
