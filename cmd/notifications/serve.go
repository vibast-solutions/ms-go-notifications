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

	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	"github.com/vibast-solutions/ms-go-notifications/config"
	"github.com/vibast-solutions/ms-go-notifications/internal/worker"
	"github.com/vibast-solutions/ms-go-notifications/pkg/provider/registry"
)

// workerShutdownTimeout bounds how long graceful shutdown waits for in-flight
// sends to finish (Business Logic §6).
const workerShutdownTimeout = 30 * time.Second

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

	// The reputation circuit breaker is shared by the worker (the dispatcher
	// reads the pause flag through it; the sweeper evaluates it every 60 s) and
	// the server (admin pause/resume endpoints and the /healthz flag). It is
	// built regardless of --no-worker so the admin API and health check work in
	// API-only mode too.
	reputation := service.NewReputationService(
		repository.NewSettingsRepository(db),
		repository.NewReputationRepository(db),
		service.ReputationConfig{
			BounceThreshold:    cfg.ReputationBounceThreshold,
			ComplaintThreshold: cfg.ReputationComplaintThreshold,
			MinSample:          cfg.ReputationMinSample,
		},
		log.StandardLogger(),
	)

	// Build the send machinery. In --no-worker mode the accept path still
	// stores messages (a worker instance elsewhere drains them); the dispatcher
	// is then a no-op and no sweeper runs.
	var dispatcher service.Dispatcher = worker.NoopDispatcher{}
	var (
		pool    *worker.Dispatcher
		sweeper *worker.Sweeper
	)
	if noWorker {
		log.Info("worker pool and sweeper disabled (--no-worker)")
	} else {
		pool, sweeper, err = buildWorker(cfg, db, reputation)
		if err != nil {
			return err
		}
		dispatcher = pool
	}

	e := newServer(cfg, db, dispatcher, reputation)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if pool != nil {
		pool.Start(ctx)
		go sweeper.Run(ctx)
	}

	log.WithField("addr", cfg.HTTPAddr).Info("starting server")
	sc := echo.StartConfig{
		Address:         cfg.HTTPAddr,
		HideBanner:      true,
		HidePort:        true,
		GracefulTimeout: workerShutdownTimeout,
	}
	err = sc.Start(ctx, e)

	// HTTP has stopped accepting; drain the worker pool, letting in-flight sends
	// finish within the timeout before the process exits.
	if pool != nil {
		log.Info("draining worker pool")
		pool.Stop(workerShutdownTimeout)
	}
	return err
}

// buildWorker wires the email provider, the bounded worker pool and the sweeper
// from config. It is only called when the worker is enabled. The dispatcher
// reads the pause flag through the reputation service (cached a few seconds) and
// the sweeper evaluates the circuit breaker every 60 s.
func buildWorker(cfg *config.Config, db *sql.DB, reputation *service.ReputationService) (*worker.Dispatcher, *worker.Sweeper, error) {
	prov, err := registry.New(context.Background(), registry.Options{
		Name:                cfg.EmailProvider,
		AWSRegion:           cfg.AWSRegion,
		SESConfigurationSet: cfg.SESConfigurationSet,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("initialising email provider: %w", err)
	}

	messages := repository.NewMessageRepository(db)
	events := repository.NewEventRepository(db)

	pool := worker.NewDispatcher(worker.DispatcherConfig{
		Concurrency: cfg.WorkerConcurrency,
		BufferSize:  cfg.DispatchBuffer,
		MaxSendRate: cfg.ProviderMaxSendRate,
		SendTimeout: workerShutdownTimeout,
	}, reputation, log.StandardLogger())

	processor := worker.NewProcessor(messages, events, prov, pool, worker.ProcessorConfig{
		MaxAttempts: cfg.SendMaxAttempts,
		BackoffBase: cfg.SendBackoffBase,
	}, log.StandardLogger())
	pool.SetProcessor(processor)

	sweeper := worker.NewSweeper(messages, pool, worker.SweeperConfig{
		Interval:       cfg.SweepInterval,
		StaleLockAfter: cfg.StaleLockAfter,
	}, log.StandardLogger())
	sweeper.SetReputationEvaluator(reputation.Evaluate)

	log.WithFields(log.Fields{
		"provider":    prov.Name(),
		"concurrency": cfg.WorkerConcurrency,
		"max_rate":    cfg.ProviderMaxSendRate,
	}).Info("worker pool enabled")

	return pool, sweeper, nil
}
