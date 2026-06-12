package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strings"

	"github.com/gen1nya/wg-admin/agent/internal/api"
	"github.com/gen1nya/wg-admin/agent/internal/devseed"
	"github.com/gen1nya/wg-admin/agent/internal/geoip"
	"github.com/gen1nya/wg-admin/agent/internal/importer"
	"github.com/gen1nya/wg-admin/agent/internal/kernel"
	"github.com/gen1nya/wg-admin/agent/internal/plan"
	"github.com/gen1nya/wg-admin/agent/internal/reconcile"
	"github.com/gen1nya/wg-admin/agent/internal/server"
	"github.com/gen1nya/wg-admin/agent/internal/store"
)

// envOr returns the environment variable's value if set and non-empty,
// otherwise def. Used to let env override a flag default.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func usage() {
	fmt.Fprintf(os.Stderr, `wg-agent — WireGuard admin agent

Usage:
  wg-agent daemon [flags]
  wg-agent import -from DIR [flags]

Flags (daemon):
  -socket   unix socket path (default /run/wg-agent.sock)
  -db       sqlite path       (default /var/lib/wg-admin/state.db)
  -geoip    MaxMind .mmdb path (default /var/lib/wg-admin/GeoLite2-City.mmdb; absent = geo off)
  -mock     use in-memory kernel instead of host state

Flags (import):
  -from        /etc/wireguard-style directory OR a single .conf file
  -db          sqlite path (default /var/lib/wg-admin/state.db)
  -only        comma-separated interface names to import (default: all)
  -public-host override PublicEndpoint for all imported interfaces
  -dry-run     parse and report; don't write
`)
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "daemon":
		if err := runDaemon(os.Args[2:]); err != nil {
			slog.Error("daemon failed", "err", err)
			os.Exit(1)
		}
	case "import":
		if err := runImport(os.Args[2:]); err != nil {
			slog.Error("import failed", "err", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	fromDir := fs.String("from", "", "source /etc/wireguard-like directory")
	dbPath := fs.String("db", "/var/lib/wg-admin/state.db", "sqlite database path")
	only := fs.String("only", "", "comma-separated interface names; empty = all")
	publicHost := fs.String("public-host", "", "override PublicEndpoint for all interfaces")
	dryRun := fs.Bool("dry-run", false, "parse and report, don't write")
	_ = fs.Parse(args)

	if *fromDir == "" {
		return fmt.Errorf("-from is required")
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	opt := importer.Options{
		FromDir:    *fromDir,
		DryRun:     *dryRun,
		PublicHost: *publicHost,
	}
	if *only != "" {
		for _, n := range strings.Split(*only, ",") {
			if n = strings.TrimSpace(n); n != "" {
				opt.Only = append(opt.Only, n)
			}
		}
	}

	stats, err := importer.Run(context.Background(), st, opt)
	if err != nil {
		return err
	}
	fmt.Printf("imported %d interfaces, %d peers (%d without stored client key)\n",
		stats.Interfaces, stats.Peers, stats.PeersNoKey)
	if len(stats.Skipped) > 0 {
		fmt.Printf("skipped: %s\n", strings.Join(stats.Skipped, ", "))
	}
	return nil
}

func runDaemon(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	socketPath := fs.String("socket", "/run/wg-agent.sock", "unix socket path")
	dbPath := fs.String("db", "/var/lib/wg-admin/state.db", "sqlite database path")
	geoipPath := fs.String("geoip", envOr("WG_ADMIN_GEOIP_DB", "/var/lib/wg-admin/GeoLite2-City.mmdb"),
		"path to a MaxMind .mmdb (GeoLite2-City/-Country); absent file = geo disabled")
	mock := fs.Bool("mock", false, "use mock kernel (no host changes)")
	_ = fs.Parse(args)

	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	var k kernel.Kernel
	if *mock {
		k = kernel.NewMock()
		if err := devseed.Seed(context.Background(), st); err != nil {
			return fmt.Errorf("dev seed: %w", err)
		}
	} else {
		k = kernel.NewReal()
	}

	engine := plan.NewEngine(st, k)
	if err := engine.Recover(context.Background()); err != nil {
		return fmt.Errorf("plan recover: %w", err)
	}

	// Post-boot reconcile: heal state that doesn't survive reboot.
	// Both steps are non-fatal — agent must come up even if the kernel is
	// in an unexpected shape, so the operator can intervene via API/CLI.
	// Skipped in mock mode: there's nothing to heal, and the devseed-based
	// kernel starts clean each time.
	if !*mock {
		reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := reconcile.Peers(reconcileCtx, st, k); err != nil {
			slog.Warn("boot: peer reconcile finished with errors", "err", err)
		}
		if err := engine.ReconcileBoot(reconcileCtx); err != nil {
			slog.Warn("boot: desired-state reconcile finished with errors", "err", err)
		}
		reconcileCancel()
	}

	geo, err := geoip.Open(*geoipPath)
	if err != nil {
		return fmt.Errorf("open geoip db %q: %w", *geoipPath, err)
	}
	defer geo.Close()
	if geo.Enabled() {
		slog.Info("geoip database loaded", "db", geo.Database(), "path", geo.Path())
	} else {
		slog.Info("geoip disabled (no database)", "path", geo.Path())
	}

	apiSrv := &api.Server{Store: st, Kernel: k, Plan: engine, Geo: geo}
	srv := server.New(server.Config{
		SocketPath: *socketPath,
		SocketMode: 0o660,
		Handler:    apiSrv.Mux(),
	})
	if err := srv.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

