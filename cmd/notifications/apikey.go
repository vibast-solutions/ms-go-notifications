package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	"github.com/vibast-solutions/ms-go-notifications/config"
)

// newAPIKeyCmd builds `notifications apikey` with the create/list/revoke/
// set-domains subcommands. Keys are managed by the operator via this CLI
// until the admin endpoints arrive (VIB-12).
func newAPIKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apikey",
		Short: "Manage API keys",
	}
	cmd.AddCommand(newAPIKeyCreateCmd(), newAPIKeyListCmd(), newAPIKeyRevokeCmd(), newAPIKeySetDomainsCmd())
	return cmd
}

// withAPIKeyService loads config, opens the DB, and runs fn with a wired
// service. Shared by all apikey subcommands.
func withAPIKeyService(fn func(ctx context.Context, keys service.APIKeys) error) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}

	keys := service.NewAPIKeyService(repository.NewAPIKeyRepository(db), cfg.AllowedDomains)
	return fn(ctx, keys)
}

func newAPIKeyCreateCmd() *cobra.Command {
	var (
		name    string
		scopes  string
		domains string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an API key and print its plaintext once",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAPIKeyService(func(ctx context.Context, keys service.APIKeys) error {
				plaintext, key, err := keys.Generate(ctx, name, splitList(scopes), splitList(domains))
				if err != nil {
					return apiKeyErr(err)
				}

				out := cmd.OutOrStdout()
				p := func(format string, args ...any) {
					_, _ = fmt.Fprintf(out, format, args...)
				}
				p("API key created.\n\n")
				p("  ID:      %s\n", key.ID)
				p("  Name:    %s\n", key.Name)
				p("  Prefix:  %s\n", key.KeyPrefix)
				p("  Scopes:  %s\n", strings.Join(key.Scopes, ", "))
				p("  Domains: %s\n\n", strings.Join(key.Domains, ", "))
				p("Plaintext key (shown only this once — store it now):\n\n  %s\n", plaintext)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "key name, 1-100 characters (required)")
	cmd.Flags().StringVar(&scopes, "scopes", "", "comma-separated scopes: send, admin (required)")
	cmd.Flags().StringVar(&domains, "domains", "", "comma-separated sender domains (required for the send scope)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("scopes")
	return cmd
}

func newAPIKeyListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAPIKeyService(func(ctx context.Context, keys service.APIKeys) error {
				all, err := keys.List(ctx)
				if err != nil {
					return err
				}

				w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
				_, _ = fmt.Fprintln(w, "ID\tNAME\tPREFIX\tSCOPES\tDOMAINS\tSTATUS\tCREATED\tLAST USED")
				for _, k := range all {
					_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
						k.ID, k.Name, k.KeyPrefix,
						strings.Join(k.Scopes, ","), strings.Join(k.Domains, ","),
						k.Status, formatTime(&k.CreatedAt), formatTime(k.LastUsedAt))
				}
				return w.Flush()
			})
		},
	}
}

func newAPIKeyRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAPIKeyService(func(ctx context.Context, keys service.APIKeys) error {
				if err := keys.Revoke(ctx, args[0]); err != nil {
					return apiKeyErr(err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "API key %s revoked.\n", args[0])
				return nil
			})
		},
	}
}

func newAPIKeySetDomainsCmd() *cobra.Command {
	var domains string

	cmd := &cobra.Command{
		Use:   "set-domains <id>",
		Short: "Replace an API key's sender domains",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAPIKeyService(func(ctx context.Context, keys service.APIKeys) error {
				if err := keys.ReplaceDomains(ctx, args[0], splitList(domains)); err != nil {
					return apiKeyErr(err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Domains of API key %s replaced.\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&domains, "domains", "", "comma-separated sender domains (required)")
	_ = cmd.MarkFlagRequired("domains")
	return cmd
}

// splitList parses a comma-separated flag into trimmed, non-empty items.
func splitList(raw string) []string {
	var items []string
	for _, part := range strings.Split(raw, ",") {
		if item := strings.TrimSpace(part); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// apiKeyErr renders domain errors in operator-friendly form.
func apiKeyErr(err error) error {
	var ve *service.ValidationError
	switch {
	case errors.As(err, &ve):
		lines := make([]string, 0, len(ve.Fields))
		for field, msg := range ve.Fields {
			lines = append(lines, fmt.Sprintf("  %s: %s", field, msg))
		}
		return fmt.Errorf("invalid input:\n%s", strings.Join(lines, "\n"))
	case errors.Is(err, service.ErrNotFound):
		return errors.New("no API key with that id")
	default:
		return err
	}
}

// formatTime renders a nullable timestamp for the list table.
func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
