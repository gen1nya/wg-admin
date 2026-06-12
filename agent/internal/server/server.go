// Package server runs the agent's HTTP server on a unix socket.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

type Config struct {
	SocketPath string      // e.g. /run/wg-agent.sock
	SocketMode os.FileMode // e.g. 0660
	GroupName  string      // optional: chown socket to this group; empty to skip
	Handler    http.Handler
}

type Server struct {
	cfg       Config
	srv       *http.Server
	lis       net.Listener
	activated bool // socket came from systemd, not self-created
}

func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

func (s *Server) Start() error {
	// Prefer a systemd socket-activated listener. systemd owns the socket
	// file, so its inode survives `systemctl restart wg-agent.service` — that
	// keeps the UI container's bind-mount valid across agent restarts (a
	// self-created socket gets a fresh inode each start and the container,
	// pinning the old one, hits ECONNREFUSED until it too is restarted).
	if lis, err := systemdListener(); err != nil {
		return fmt.Errorf("systemd socket activation: %w", err)
	} else if lis != nil {
		s.lis = lis
		s.activated = true
		slog.Info("agent listening (systemd socket-activated)", "socket", s.cfg.SocketPath)
		s.serve()
		return nil
	}

	// Fallback: create and own the socket ourselves (dev, -mock, or hosts
	// without the .socket unit).
	if err := os.MkdirAll(filepath.Dir(s.cfg.SocketPath), 0o755); err != nil {
		return fmt.Errorf("mkdir socket dir: %w", err)
	}
	if err := os.Remove(s.cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	lis, err := net.Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	mode := s.cfg.SocketMode
	if mode == 0 {
		mode = 0o660
	}
	if err := os.Chmod(s.cfg.SocketPath, mode); err != nil {
		_ = lis.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.lis = lis
	slog.Info("agent listening", "socket", s.cfg.SocketPath, "mode", fmt.Sprintf("%#o", mode))
	s.serve()
	return nil
}

// serve wires up the HTTP server on s.lis and starts accepting in the background.
func (s *Server) serve() {
	s.srv = &http.Server{
		Handler:           s.cfg.Handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := s.srv.Serve(s.lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve error", "err", err)
		}
	}()
}

// systemdListener returns the unix listener passed by systemd socket
// activation, or nil if the agent wasn't socket-activated. It implements the
// sd_listen_fds(3) protocol directly so we don't pull in a dependency: systemd
// sets LISTEN_PID to our pid and LISTEN_FDS to the count of fds starting at 3.
func systemdListener() (net.Listener, error) {
	if pid, err := strconv.Atoi(os.Getenv("LISTEN_PID")); err != nil || pid != os.Getpid() {
		return nil, nil
	}
	nfds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || nfds < 1 {
		return nil, nil
	}
	// Consume the env so child processes (wg/ip/ipset shell-outs) don't inherit it.
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")
	if nfds > 1 {
		slog.Warn("systemd passed >1 fd; using the first", "count", nfds)
	}
	const fd = 3 // SD_LISTEN_FDS_START
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "wg-agent-systemd-socket")
	if f == nil {
		return nil, fmt.Errorf("fd %d invalid", fd)
	}
	defer f.Close() // FileListener dups the fd; the os.File is no longer needed
	lis, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("fd %d is not a listening socket: %w", fd, err)
	}
	if _, ok := lis.(*net.UnixListener); !ok {
		return nil, fmt.Errorf("fd %d is not a unix socket (%T)", fd, lis)
	}
	return lis, nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}
