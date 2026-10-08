package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"dota-doggo/internal/config"
	"dota-doggo/internal/service"
	"dota-doggo/internal/store/postgres"
)

type importOptions struct {
	Path  string
	Topic service.LegacyTopic
}

func parseImport(args []string, out io.Writer) (importOptions, error) {
	var o importOptions
	flags := flag.NewFlagSet("import-legacy", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&o.Path, "path", "old_code/players.json", "Legacy players JSON path")
	flags.Int64Var(&o.Topic.ChatID, "chat-id", 0, "Telegram chat ID (required)")
	var thread int64
	var title string
	flags.Int64Var(&thread, "thread-id", 0, "Telegram topic ID (optional)")
	flags.StringVar(&title, "title", "", "Topic title (optional)")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 || o.Topic.ChatID == 0 || o.Path == "" {
		return o, errors.New("usage: dota-doggo import-legacy --chat-id <id> [--path <file>] [--thread-id <id>] [--title <title>]")
	}
	var err error
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "thread-id" {
			if thread <= 0 {
				err = errors.New("thread-id must be positive")
			} else {
				o.Topic.ThreadID = &thread
			}
		}
		if f.Name == "title" {
			o.Topic.Title = &title
		}
	})
	return o, err
}
func runImport(ctx context.Context, cfg config.Config, o importOptions, out io.Writer) error {
	file, err := os.Open(o.Path)
	if err != nil {
		return fmt.Errorf("open legacy file: %w", err)
	}
	players, err := service.ParseLegacy(file)
	_ = file.Close()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pool, err := openApplicationDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	o.Topic.Timezone = cfg.DefaultTimezone
	var inserted int
	err = postgres.InTx(ctx, pool, func(tx *postgres.Store) error {
		var err error
		inserted, err = service.ImportLegacy(ctx, tx, o.Topic, players)
		return err
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Imported %d players.\n", inserted)
	return err
}
