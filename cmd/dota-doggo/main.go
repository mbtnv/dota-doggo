package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dota-doggo/internal/config"
	"dota-doggo/internal/health"
	"dota-doggo/internal/logging"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/migrations"
	"github.com/joho/godotenv"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, logging.Redact(err.Error(), os.Getenv("BOT_TOKEN"), os.Getenv("OPENDOTA_API_KEY"), os.Getenv("DATABASE_URL"), os.Getenv("TELEGRAM_PROXY_URL")))
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(out, "dota-doggo\n\nCommands:\n  bot                  Run Telegram polling and commands\n  worker               Poll matches and send automatic reports\n  import-legacy        Import old players JSON (--help for options)\n  migrate [up|status]  Create or adopt the compatible PostgreSQL schema\n  healthcheck          Check PostgreSQL connectivity\n  healthcheck bot|worker  Check process readiness at HEALTH_ADDR")
		return err
	}
	if args[0] != "migrate" && args[0] != "healthcheck" && args[0] != "bot" && args[0] != "worker" && args[0] != "import-legacy" {
		return fmt.Errorf("unknown command %q; use --help", args[0])
	}
	if args[0] == "healthcheck" && (len(args) > 2 || (len(args) == 2 && args[1] != "bot" && args[1] != "worker")) {
		return errors.New("usage: dota-doggo healthcheck [bot|worker]")
	}
	if args[0] == "bot" && len(args) > 1 {
		return errors.New("usage: dota-doggo bot")
	}
	if args[0] == "worker" && len(args) > 1 {
		return errors.New("usage: dota-doggo worker")
	}
	var importArgs importOptions
	if args[0] == "import-legacy" {
		var err error
		importArgs, err = parseImport(args[1:], out)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	action := "up"
	if args[0] == "migrate" {
		if len(args) > 2 {
			return errors.New("usage: dota-doggo migrate [up|status]")
		}
		if len(args) == 2 {
			action = args[1]
		}
		if action != "up" && action != "status" {
			return errors.New("usage: dota-doggo migrate [up|status]")
		}
	}
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("could not read .env")
	}
	if args[0] == "healthcheck" && len(args) == 2 {
		if err := health.Probe(ctx, os.Getenv("HEALTH_ADDR"), args[1]); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "%s ready\n", args[1])
		return err
	}
	cfg, err := config.Load(os.LookupEnv, args[0] == "bot" || args[0] == "worker")
	if err != nil {
		return err
	}
	log := logging.New(os.Stderr, cfg.LogLevel, cfg.BotToken, cfg.OpenDotaAPIKey, cfg.DatabaseURL, cfg.TelegramProxyURL)
	if args[0] == "bot" {
		return runBot(ctx, cfg, log)
	}
	if args[0] == "worker" {
		return runWorker(ctx, cfg, log)
	}
	if args[0] == "import-legacy" {
		return runImport(ctx, cfg, importArgs, out)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if args[0] == "healthcheck" {
		pool, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
		if err != nil {
			return err
		}
		defer pool.Close()
		_, err = fmt.Fprintln(out, "PostgreSQL connection OK (connectivity only)")
		return err
	}
	db, err := migrations.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if action == "up" {
		if err := migrations.Up(ctx, db); err != nil {
			return err
		}
		log.Info("database migrations complete")
	}
	v, err := migrations.Version(ctx, db)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Schema version: %d\n", v)
	return err
}
