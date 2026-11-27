package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/itsmepava/container/internal/app"
	"github.com/itsmepava/container/internal/config"
	"github.com/itsmepava/container/internal/db"
	"github.com/itsmepava/container/internal/files"
)

// runDemo starts a private PostgreSQL inside the data folder (downloaded once),
// loads the demo data on first run, and serves the app. Nothing is installed
// system-wide and no sudo is needed.
func runDemo(ctx context.Context, cfg config.Config, args []string, log *slog.Logger) error {
	fl := flag.NewFlagSet("demo", flag.ExitOnError)
	port := fl.Int("port", 8080, "port for the web app")
	reset := fl.Bool("reset", false, "wipe the demo data and start fresh")
	_ = fl.Parse(args)

	if os.Geteuid() == 0 {
		return fmt.Errorf("run the demo as your normal user, not root or sudo (PostgreSQL refuses to run as root)")
	}
	dir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return err
	}
	if *reset {
		_ = os.RemoveAll(filepath.Join(dir, "pg-data"))
		_ = os.RemoveAll(filepath.Join(dir, "files"))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	fmt.Println("Starting the built-in database (the first run downloads PostgreSQL, about 30 MB)…")
	const pgPort = 54329
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(pgPort).
		Username("eimsky").Password("eimsky").Database("eimsky").
		RuntimePath(filepath.Join(dir, "pg-runtime")).
		DataPath(filepath.Join(dir, "pg-data")).
		CachePath(filepath.Join(dir, "pg-cache")).
		Logger(io.Discard))
	if err := pg.Start(); err != nil {
		return fmt.Errorf("start the built-in database: %w (if port %d is busy, another demo may still be running)", err, pgPort)
	}
	defer func() {
		fmt.Println("Stopping the built-in database…")
		_ = pg.Stop()
	}()

	cfg.DatabaseURL = fmt.Sprintf("postgres://eimsky:eimsky@localhost:%d/eimsky?sslmode=disable", pgPort)
	cfg.Addr = fmt.Sprintf(":%d", *port)
	if os.Getenv("BASE_URL") == "" {
		cfg.BaseURL = fmt.Sprintf("http://localhost:%d", *port)
	}
	cfg.DevMode = true

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	fs, err := files.New(filepath.Join(dir, "files"), cfg.FileKey)
	if err != nil {
		return err
	}
	a := app.New(pool, fs, cfg, log)
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	var users int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users)
	if users == 0 {
		fmt.Println("Loading demo data…")
		if err := a.SeedDemo(ctx); err != nil {
			return err
		}
	} else if err := a.SeedReference(ctx); err != nil {
		return err
	}

	fmt.Printf(`
  Eimsky is ready:  %s

  Sign in with password  %s
    factory@eimsky.test   factory portal
    ops@eimsky.test       pools, load planner, bids
    admin@eimsky.test     everything
    jaaf@eimsky.test      insights

  Press Ctrl+C to stop. Your demo data is kept in %s
  (run with --reset to start fresh).

`, cfg.BaseURL, app.DemoPassword, dir)
	return serve(ctx, a, cfg, log)
}
