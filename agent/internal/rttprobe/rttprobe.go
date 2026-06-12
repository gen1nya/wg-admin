// Package rttprobe measures round-trip time to connected WireGuard peers by
// pinging their *tunnel* IP (the /32 from allowed-ips). The ICMP echo travels
// inside the encrypted tunnel, so a reply is the real tunnel RTT — but only for
// clients whose OS answers ICMP (Android/iOS/Linux). Windows clients usually
// drop echo on the WireGuard adapter by default, so they read as N/A. This is
// the only RTT a server can obtain passively; it is reported honestly, never as
// authoritative device latency.
//
// Probing is server-side and best-effort: it pings only our own tunnel /32s
// (never arbitrary addresses), skips peers idle beyond a threshold, and a miss
// (timeout) simply leaves no sample.
package rttprobe

import (
	"context"
	"log/slog"
	"net/netip"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gen1nya/wg-admin/agent/internal/kernel"
)

const (
	defaultInterval      = 5 * time.Minute
	probeMaxHandshakeAge = 900 // seconds; don't ping peers idle longer than this
	pingTimeout          = 1 * time.Second
	maxConcurrent        = 8
)

// Sample is the most recent RTT measurement for a peer.
type Sample struct {
	RTTms      float64 // round-trip milliseconds; valid only when OK
	MeasuredAt int64   // unix seconds when measured
	OK         bool    // a reply came back
}

// Prober keeps a live RTT cache keyed by peer public key, refreshed on a ticker.
type Prober struct {
	k        kernel.Kernel
	interval time.Duration

	mu    sync.RWMutex
	cache map[string]Sample
}

// New builds a Prober. A non-positive interval falls back to the default.
func New(k kernel.Kernel, interval time.Duration) *Prober {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Prober{k: k, interval: interval, cache: map[string]Sample{}}
}

// Get returns the last sample for a peer's public key. Nil-safe so a Server
// constructed without a prober can call it freely.
func (p *Prober) Get(pubkey string) (Sample, bool) {
	if p == nil {
		return Sample{}, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	s, ok := p.cache[pubkey]
	return s, ok
}

// Run probes on a ticker until ctx is cancelled. Blocking — call in a goroutine.
func (p *Prober) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	p.probeOnce(ctx) // warm the cache immediately
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.probeOnce(ctx)
		}
	}
}

type target struct {
	pubkey string
	ip     netip.Addr
}

func (p *Prober) probeOnce(ctx context.Context) {
	targets := p.collectTargets()
	if len(targets) == 0 {
		return
	}
	now := time.Now().Unix()
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for _, tg := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(tg target) {
			defer wg.Done()
			defer func() { <-sem }()
			rtt, ok := ping(ctx, tg.ip)
			p.mu.Lock()
			p.cache[tg.pubkey] = Sample{RTTms: rtt, MeasuredAt: now, OK: ok}
			p.mu.Unlock()
		}(tg)
	}
	wg.Wait()
	p.prune(now)
}

// collectTargets lists peers worth probing: a recent handshake (so the tunnel
// is plausibly warm) and a host tunnel IP to aim at.
func (p *Prober) collectTargets() []target {
	names, err := p.k.ListInterfaces()
	if err != nil {
		slog.Debug("rttprobe: list interfaces", "err", err)
		return nil
	}
	now := time.Now().Unix()
	var out []target
	for _, name := range names {
		st, err := p.k.ShowInterface(name)
		if err != nil {
			continue
		}
		for _, peer := range st.Peers {
			if peer.LatestHandshake == 0 || now-peer.LatestHandshake > probeMaxHandshakeAge {
				continue
			}
			ip, ok := tunnelIP(peer.AllowedIPs)
			if !ok {
				continue
			}
			out = append(out, target{pubkey: peer.PublicKey, ip: ip})
		}
	}
	return out
}

// prune drops samples older than a few intervals so the cache doesn't grow with
// peers that have since left.
func (p *Prober) prune(now int64) {
	cutoff := now - int64(5*p.interval/time.Second)
	p.mu.Lock()
	for k, s := range p.cache {
		if s.MeasuredAt < cutoff {
			delete(p.cache, k)
		}
	}
	p.mu.Unlock()
}

// tunnelIP picks a single host address to ping from a comma-separated
// allowed-ips list. A v4 /32 wins; otherwise the first v6 /128. A bare subnet
// (non-host mask) is unpingable and yields ok=false.
func tunnelIP(allowed string) (netip.Addr, bool) {
	var v6 netip.Addr
	var haveV6 bool
	for _, part := range strings.Split(allowed, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(part)
		if err != nil {
			continue
		}
		addr := pfx.Addr().Unmap()
		if addr.Is4() && pfx.Bits() == 32 {
			return addr, true
		}
		if addr.Is6() && pfx.Bits() == 128 && !haveV6 {
			v6, haveV6 = addr, true
		}
	}
	if haveV6 {
		return v6, true
	}
	return netip.Addr{}, false
}

var pingRe = regexp.MustCompile(`time[=<]([0-9.]+)`)

// ping sends one ICMP echo and returns the RTT in ms. The tunnel route sends it
// through the right wg interface, so no interface binding is needed.
func ping(ctx context.Context, ip netip.Addr) (float64, bool) {
	cctx, cancel := context.WithTimeout(ctx, pingTimeout+500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ping", "-n", "-c", "1", "-W", "1", ip.String())
	out, err := cmd.Output()
	if err != nil {
		return 0, false // timeout, unreachable, or no reply
	}
	return parseRTT(out)
}

// parseRTT extracts the round-trip ms from `ping` output (factored out for
// deterministic testing).
func parseRTT(out []byte) (float64, bool) {
	m := pingRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	rtt, err := strconv.ParseFloat(string(m[1]), 64)
	if err != nil {
		return 0, false
	}
	return rtt, true
}
