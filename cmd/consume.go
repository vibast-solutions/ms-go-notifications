package cmd

import (
	"context"
	"database/sql"
	"os"
	"os/signal"
	"syscall"

	"github.com/vibast-solutions/ms-go-notifications/app/lock"
	"github.com/vibast-solutions/ms-go-notifications/app/preparer"
	"github.com/vibast-solutions/ms-go-notifications/app/queue"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	"github.com/vibast-solutions/ms-go-notifications/config"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var consumeCmd = &cobra.Command{
	Use:   "consume",
	Short: "Consume queued messages",
	Long:  "Consume queued messages from Redis streams.",
}

// init registers consume subcommands.
func init() {
	consumeCmd.AddCommand(consumeEmailsCmd)
	rootCmd.AddCommand(consumeCmd)
}

var consumeEmailsCmd = &cobra.Command{
	Use:   "emails [consumer_name]",
	Short: "Start the email queue consumer",
	Long:  "Start a worker that reads email messages from the Redis stream and sends them via SES.",
	Args:  cobra.ExactArgs(1),
	Run:   runConsumeEmails,
}

// runConsumeEmails starts the email queue consumer worker.
func runConsumeEmails(_ *cobra.Command, args []string) {
	consumerName := args[0]

	cfg, err := config.Load()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load configuration")
	}
	if err := configureLogging(cfg); err != nil {
		logrus.WithError(err).Fatal("Failed to configure logging")
	}

	db, err := sql.Open("mysql", cfg.MySQL.DSN)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to connect to database")
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.MySQL.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MySQL.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MySQL.ConnMaxLifetime)

	if err := db.Ping(); err != nil {
		logrus.WithError(err).Fatal("Failed to ping database")
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer rdb.Close()

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		logrus.WithError(err).Fatal("Failed to connect to Redis")
	}

	emailProvider, err := buildEmailProvider(cfg)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to build email provider")
	}

	emailPreparer := preparer.NewChain(preparer.NewRawPreparer(cfg.EmailProviders.AWS.SourceEmail))
	emailHistory := repository.NewEmailHistoryRepository(db)
	locker := lock.NewRedisLocker(rdb)
	emailService := service.NewEmailService(emailPreparer, emailProvider, emailHistory, locker)

	consumer := queue.NewEmailConsumer(rdb, emailService, consumerName)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		logrus.Info("Received shutdown signal, stopping consumer...")
		cancel()
	}()

	if err := consumer.Run(ctx); err != nil {
		logrus.WithError(err).Fatal("Consumer error")
	}

	logrus.Info("Consumer stopped")
}
