package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"

	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/config"
)

// cleanup targets and retention policy (Business Logic §9).
const (
	cleanupTargetAttachments = "attachments"
	cleanupTargetHistory     = "history"

	cleanupMinAge         = 24 * time.Hour  // safety floor: refuse anything smaller
	defaultAttachmentsAge = 168 * time.Hour // 7 days
	defaultHistoryAge     = 720 * time.Hour // 30 days

	cleanupBatchPause = 100 * time.Millisecond // short breather between batches
)

// cleanupRepo is the persistence the cleanup loop needs; satisfied by
// *repository.MessageRepository.
type cleanupRepo interface {
	PurgeAttachmentsBatch(ctx context.Context, cutoff time.Time, limit int, now time.Time) (int64, error)
	DeleteHistoryBatch(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

// newCleanupCmd builds `notifications cleanup`, the retention cron command.
func newCleanupCmd() *cobra.Command {
	var (
		target    string
		olderThan time.Duration
		batch     int
	)

	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Purge old attachments or delete old message history (cron)",
		Long: "Retention maintenance. --target attachments nulls attachment content of\n" +
			"messages that finished before the cutoff (default 7d); --target history\n" +
			"deletes whole messages (with recipients, events, attachments) created\n" +
			"before the cutoff (default 30d). --older-than below 24h is refused.",
		RunE: func(cmd *cobra.Command, args []string) error {
			age, err := resolveCleanupArgs(target, olderThan, cmd.Flags().Changed("older-than"), batch)
			if err != nil {
				return err
			}
			return withCleanupRepo(func(ctx context.Context, repo cleanupRepo) error {
				now := time.Now().UTC().Truncate(time.Millisecond)
				total, err := runCleanupLoop(ctx, repo, target, now.Add(-age), now, batch, cmd.OutOrStdout())
				if err != nil {
					return err
				}
				printCleanupSummary(cmd.OutOrStdout(), target, total)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "what to clean: attachments | history (required)")
	cmd.Flags().DurationVar(&olderThan, "older-than", 0,
		"age cutoff, e.g. 168h (default 168h for attachments, 720h for history; minimum 24h)")
	cmd.Flags().IntVar(&batch, "batch", 1000, "rows per batch")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

// resolveCleanupArgs validates the flags and returns the effective age cutoff,
// applying the per-target default when --older-than was not set.
func resolveCleanupArgs(target string, olderThan time.Duration, olderThanSet bool, batch int) (time.Duration, error) {
	switch target {
	case cleanupTargetAttachments:
		if !olderThanSet {
			olderThan = defaultAttachmentsAge
		}
	case cleanupTargetHistory:
		if !olderThanSet {
			olderThan = defaultHistoryAge
		}
	case "":
		return 0, errors.New("--target is required (attachments | history)")
	default:
		return 0, fmt.Errorf("unknown --target %q (want attachments | history)", target)
	}
	if olderThan < cleanupMinAge {
		return 0, fmt.Errorf("--older-than must be at least %s (got %s)", cleanupMinAge, olderThan)
	}
	if batch < 1 {
		return 0, fmt.Errorf("--batch must be positive (got %d)", batch)
	}
	return olderThan, nil
}

// runCleanupLoop repeatedly processes batches until a short batch signals the
// backlog is drained, returning the total rows affected. It pauses briefly
// between batches to spare the database.
func runCleanupLoop(ctx context.Context, repo cleanupRepo, target string, cutoff, now time.Time, batch int, out io.Writer) (int64, error) {
	var total int64
	for {
		var (
			n   int64
			err error
		)
		switch target {
		case cleanupTargetAttachments:
			n, err = repo.PurgeAttachmentsBatch(ctx, cutoff, batch, now)
		case cleanupTargetHistory:
			n, err = repo.DeleteHistoryBatch(ctx, cutoff, batch)
		default:
			return total, fmt.Errorf("unknown --target %q", target)
		}
		if err != nil {
			return total, err
		}
		total += n
		if n < int64(batch) {
			return total, nil // fewer than a full batch → nothing left
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(cleanupBatchPause):
		}
	}
}

func printCleanupSummary(out io.Writer, target string, total int64) {
	switch target {
	case cleanupTargetAttachments:
		_, _ = fmt.Fprintf(out, "Purged %d attachment(s).\n", total)
	case cleanupTargetHistory:
		_, _ = fmt.Fprintf(out, "Deleted %d message(s) with their recipients, events and attachments.\n", total)
	}
}

// withCleanupRepo loads config, opens the DB, and runs fn with a wired
// repository, mirroring the apikey subcommands' setup.
func withCleanupRepo(fn func(ctx context.Context, repo cleanupRepo) error) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	configureLogging(cfg)

	db, err := sql.Open("mysql", cfg.DatabaseDSN)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	return fn(ctx, repository.NewMessageRepository(db))
}
