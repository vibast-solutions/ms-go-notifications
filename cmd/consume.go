package cmd

import (
	"context"
	"database/sql"
	"log"
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
		log.Fatalf("Failed to load configuration: %v", err)
	}

	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.MySQLMaxOpen)
	db.SetMaxIdleConns(cfg.MySQLMaxIdle)
	db.SetConnMaxLifetime(cfg.MySQLMaxLife)

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer rdb.Close()

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}

	emailProvider, err := buildEmailProvider(cfg)
	if err != nil {
		log.Fatalf("Failed to build email provider: %v", err)
	}

	emailPreparer := preparer.NewChain(preparer.NewRawPreparer(cfg.SESSourceEmail))
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
		log.Println("Received shutdown signal, stopping consumer...")
		cancel()
	}()

	if err := consumer.Run(ctx); err != nil {
		log.Fatalf("Consumer error: %v", err)
	}

	log.Println("Consumer stopped")
}
