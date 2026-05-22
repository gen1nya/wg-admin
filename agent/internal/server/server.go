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
	"time"
)

type Config struct {
	SocketPath string      // e.g. /run/wg-agent.sock
	SocketMode os.FileMode // e.g. 0660
	GroupName  string      // optional: chown socket to this group; empty to skip
	Handler    http.Handler
}

type Server struct {
	cfg    Config
	srv    *http.Server
	lis    net.Listener
}

func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

func (s *Server) Start() error {
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
	s.srv = &http.Server{
		Handler:           s.cfg.Handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	slog.Info("agent listening", "socket", s.cfg.SocketPath, "mode", fmt.Sprintf("%#o", mode))
	go func() {
		if err := s.srv.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve error", "err", err)
		}
	}()
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}
