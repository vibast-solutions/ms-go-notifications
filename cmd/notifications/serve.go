package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v5"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/vibast-solutions/ms-go-notifications/config"
)

func newServeCmd() *cobra.Command {
	var noWorker bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(noWorker)
		},
	}
	cmd.Flags().BoolVar(&noWorker, "no-worker", false,
		"run the HTTP API only, without the in-process worker pool and sweeper")

	return cmd
}

func runServe(noWorker bool) error {
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
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(5 * time.Minute)

	// The server starts even when MySQL is unreachable so /healthz can
	// report the outage (503) instead of the process crash-looping.
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		log.WithError(err).Warn("database unreachable at startup; /healthz will report 503")
	} else {
		log.Info("connected to MySQL")
	}

	if noWorker {
		log.Info("worker pool and sweeper disabled (--no-worker)")
	}
	// The dispatcher, worker pool and sweeper are wired here in VIB-9.

	e := newServer(cfg, db)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.WithField("addr", cfg.HTTPAddr).Info("starting server")
	sc := echo.StartConfig{
		Address:         cfg.HTTPAddr,
		HideBanner:      true,
		HidePort:        true,
		GracefulTimeout: 30 * time.Second,
	}
	return sc.Start(ctx, e)
}
