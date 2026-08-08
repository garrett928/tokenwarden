// Command tokenwardend is the tokenwarden daemon: it owns the job store and
// serves the HTTP API. Phase 3 added internal/runner and internal/dispatch,
// so a job can run on demand — via POST /api/jobs/{id}/dispatch or
// `tokenwarden queue dispatch`. Phase 5 adds internal/scheduler's
// budget-aware dispatch loop on top of that: it always runs, but only
// ever dispatches once a user opts in via `tokenwarden scheduler config
// set --enabled` (or PUT /api/scheduler/config) — see CLAUDE.md for the
// module layout and docs/REQUIREMENTS.md for where this is headed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tokenwarden/internal/api"
	"tokenwarden/internal/backfill"
	"tokenwarden/internal/budget"
	"tokenwarden/internal/config"
	"tokenwarden/internal/dispatch"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/scheduler"
	"tokenwarden/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tokenwardend:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to config.json (default: OS-appropriate config dir)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if err := cfg.EnsureDataDir(); err != nil {
		return err
	}

	st, err := store.Open(cfg.ResolvedDBPath())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	q := queue.New(st)
	rnr := runner.New(cfg.ClaudeBinaryPath)
	ledger := budget.New(st)
	go func() {
		dir, err := backfill.DefaultProjectsDir()
		if err != nil {
			log.Printf("backfill: resolving projects dir: %v", err)
			return
		}
		stats, err := backfill.IndexProjects(context.Background(), ledger, dir)
		if err != nil {
			log.Printf("backfill: indexing %s: %v", dir, err)
			return
		}
		log.Printf("backfill: scanned %d files, %d lines, inserted %d usage entries (%d skipped/duplicate)",
			stats.FilesScanned, stats.LinesScanned, stats.EntriesInserted, stats.EntriesSkipped)
	}()
	disp := dispatch.New(q, rnr, ledger)

	// The dispatch loop (Phase 5, REQUIREMENTS.md §6.2) always runs — it's
	// a no-op every tick until a user enables it via `tokenwarden
	// scheduler config set --enabled` (or PUT /api/scheduler/config),
	// since Engine.Tick checks SchedulerConfig.Enabled itself.
	sched := scheduler.New(st, q, disp, ledger, nil)
	go sched.Run(ctx)

	handler := api.NewServer(st, q, disp, ledger)
	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
		// A malicious or misbehaving client sending headers slowly
		// shouldn't be able to tie up a connection indefinitely, even
		// though this only ever binds to loopback (NFR-SEC-2).
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("tokenwardend listening on %s (data: %s)", cfg.ListenAddr, cfg.DataDir)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		log.Println("shutdown signal received, draining connections...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down http server: %w", err)
		}
		<-serveErr
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}
}
