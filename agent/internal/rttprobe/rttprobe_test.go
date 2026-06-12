package rttprobe

import (
	"net/netip"
	"testing"
)

func TestTunnelIP(t *testing.T) {
	cases := []struct {
		name    string
		allowed string
		want    string
		ok      bool
	}{
		{"v4 host", "10.8.1.5/32", "10.8.1.5", true},
		{"v4 host with v6", "10.8.1.5/32,fd00::5/128", "10.8.1.5", true},
		{"v6 host only", "fd00::5/128", "fd00::5", true},
		{"v6 preferred order keeps first", "fd00::5/128,fd00::6/128", "fd00::5", true},
		{"v4 prefers over leading v6", "fd00::5/128,10.8.1.5/32", "10.8.1.5", true},
		{"subnet not host", "10.8.1.0/24", "", false},
		{"empty", "", "", false},
		{"garbage", "not-a-cidr", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := tunnelIP(c.allowed)
			if ok != c.ok {
				t.Fatalf("ok=%v, want %v", ok, c.ok)
			}
			if ok && got != netip.MustParseAddr(c.want) {
				t.Errorf("ip=%s, want %s", got, c.want)
			}
		})
	}
}

func TestParseRTT(t *testing.T) {
	linux := []byte(`PING 10.8.1.5 (10.8.1.5) 56(84) bytes of data.
64 bytes from 10.8.1.5: icmp_seq=1 ttl=64 time=23.4 ms

--- 10.8.1.5 ping statistics ---
1 packets transmitted, 1 received, 0% packet loss, time 0ms`)
	if rtt, ok := parseRTT(linux); !ok || rtt != 23.4 {
		t.Errorf("linux: rtt=%v ok=%v, want 23.4 true", rtt, ok)
	}

	subMs := []byte("64 bytes from 127.0.0.1: icmp_seq=1 ttl=64 time<0.1 ms")
	if rtt, ok := parseRTT(subMs); !ok || rtt != 0.1 {
		t.Errorf("sub-ms: rtt=%v ok=%v, want 0.1 true", rtt, ok)
	}

	timeout := []byte(`PING 10.8.1.9 (10.8.1.9) 56(84) bytes of data.

--- 10.8.1.9 ping statistics ---
1 packets transmitted, 0 received, 100% packet loss, time 0ms`)
	if _, ok := parseRTT(timeout); ok {
		t.Error("timeout output parsed as a hit")
	}
}

func TestProberGetNilSafe(t *testing.T) {
	var p *Prober
	if _, ok := p.Get("anykey"); ok {
		t.Error("nil prober returned a sample")
	}
}
