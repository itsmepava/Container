// Command eimsky runs the Eimsky server and its maintenance tasks.
//
//	eimsky serve            run the web app, mobile API and background jobs (default)
//	eimsky migrate          apply database migrations
//	eimsky seed [--reset]   load the demo data (--reset wipes the database first)
//	eimsky create-admin EMAIL PASSWORD   create the first admin on an empty database
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/itsmepava/container/internal/api"
	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/config"
	"github.com/itsmepava/container/internal/db"
	"github.com/itsmepava/container/internal/files"
	"github.com/itsmepava/container/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cmd := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	if err := run(cmd, args, log); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func run(cmd string, args []string, log *slog.Logger) error {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	fs, err := files.New(filepath.Join(cfg.DataDir, "files"), cfg.FileKey)
	if err != nil {
		return err
	}
	a := app.New(pool, fs, cfg, log)

	switch cmd {
	case "migrate":
		if err := db.Migrate(ctx, pool); err != nil {
			return err
		}
		log.Info("migrations applied")
		return a.SeedReference(ctx)
	case "seed":
		fl := flag.NewFlagSet("seed", flag.ExitOnError)
		reset := fl.Bool("reset", false, "wipe the database before seeding")
		_ = fl.Parse(args)
		if *reset {
			if err := db.Reset(ctx, pool); err != nil {
				return err
			}
		}
		if err := db.Migrate(ctx, pool); err != nil {
			return err
		}
		if err := a.SeedDemo(ctx); err != nil {
			return err
		}
		log.Info("demo data loaded", "password", app.DemoPassword, "accounts", "factory@ ops@ admin@ jaaf@ eimsky.test")
		return nil
	case "create-admin":
		if len(args) != 2 {
			return errors.New("usage: eimsky create-admin EMAIL PASSWORD")
		}
		if err := db.Migrate(ctx, pool); err != nil {
			return err
		}
		if err := a.SeedReference(ctx); err != nil {
			return err
		}
		return a.EnsureAdmin(ctx, args[0], args[1])
	case "serve":
		if err := db.Migrate(ctx, pool); err != nil {
			return err
		}
		if err := a.SeedReference(ctx); err != nil {
			return err
		}
		srv, err := web.New(a)
		if err != nil {
			return err
		}
		srv.API = api.New(a).Handler()
		every := 30 * time.Second
		if d, err := time.ParseDuration(os.Getenv("JOBS_INTERVAL")); err == nil && d > 0 {
			every = d
		}
		go a.RunJobs(ctx, every)
		hs := &http.Server{Addr: cfg.Addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = hs.Shutdown(sctx)
		}()
		log.Info("Eimsky is running", "url", cfg.BaseURL, "addr", cfg.Addr, "dev_mode", cfg.DevMode)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	return fmt.Errorf("unknown command %q (use serve, migrate, seed or create-admin)", cmd)
}
