// Package wgconf parses wg-quick style .conf files.
//
// WireGuard .conf is INI-ish: sections in square brackets, Key = Value pairs
// per section, # comments. wg-quick adds pseudo-directives (PostUp, PostDown,
// Table, Address, DNS, MTU); we preserve the ones we care about and ignore
// the rest.
package wgconf

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Peer struct {
	PublicKey    string
	AllowedIPs   string
	Endpoint     string
	Keepalive    int
	PresharedKey string
	// Names collects consecutive comments that immediately precede this
	// [Peer] block. Multiple may accumulate from past edits; callers pick
	// the most trustworthy one (usually the last).
	Names []string
}

type Config struct {
	// [Interface] section
	Address    string
	ListenPort int
	PrivateKey string
	DNS        string
	MTU        int
	PostUp     []string
	PostDown   []string

	Peers []Peer
}

// Parse reads a .conf stream.
func Parse(r io.Reader) (*Config, error) {
	cfg := &Config{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	section := ""        // "interface" | "peer" | ""
	var currentPeer *Peer // populated while section=="peer"
	pendingComments := []string{}
	commitPeer := func() {
		if currentPeer != nil {
			cfg.Peers = append(cfg.Peers, *currentPeer)
			currentPeer = nil
		}
	}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			comment := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			pendingComments = append(pendingComments, comment)
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			commitPeer()
			sec := strings.ToLower(strings.Trim(line, "[]"))
			switch sec {
			case "interface":
				section = "interface"
				pendingComments = nil
			case "peer":
				section = "peer"
				currentPeer = &Peer{Names: append([]string(nil), pendingComments...)}
				pendingComments = nil
			default:
				section = sec
			}
			continue
		}

		k, v, ok := splitKV(line)
		if !ok {
			continue // not a Key = Value line; ignore
		}

		switch section {
		case "interface":
			switch strings.ToLower(k) {
			case "address":
				cfg.Address = v
			case "listenport":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.ListenPort = n
				}
			case "privatekey":
				cfg.PrivateKey = v
			case "dns":
				cfg.DNS = v
			case "mtu":
				if n, err := strconv.Atoi(v); err == nil {
					cfg.MTU = n
				}
			case "postup":
				cfg.PostUp = append(cfg.PostUp, v)
			case "postdown":
				cfg.PostDown = append(cfg.PostDown, v)
			}
		case "peer":
			if currentPeer == nil {
				continue
			}
			switch strings.ToLower(k) {
			case "publickey":
				currentPeer.PublicKey = v
			case "allowedips":
				currentPeer.AllowedIPs = v
			case "endpoint":
				currentPeer.Endpoint = v
			case "persistentkeepalive":
				if n, err := strconv.Atoi(v); err == nil {
					currentPeer.Keepalive = n
				}
			case "presharedkey":
				currentPeer.PresharedKey = v
			}
			// peer-comment pairing already captured at [Peer] — ignore
			// pendingComments accumulated inside the peer block.
			pendingComments = nil
		}
	}
	commitPeer()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return cfg, nil
}

func ParseFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

func splitKV(line string) (string, string, bool) {
	i := strings.IndexByte(line, '=')
	if i < 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:i])
	v := strings.TrimSpace(line[i+1:])
	if k == "" {
		return "", "", false
	}
	return k, v, true
}
