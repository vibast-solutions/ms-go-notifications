package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vibast-solutions/ms-go-notifications/app/controller"
	grpcserver "github.com/vibast-solutions/ms-go-notifications/app/grpc"
	"github.com/vibast-solutions/ms-go-notifications/app/lock"
	"github.com/vibast-solutions/ms-go-notifications/app/preparer"
	"github.com/vibast-solutions/ms-go-notifications/app/provider"
	"github.com/vibast-solutions/ms-go-notifications/app/queue"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	types "github.com/vibast-solutions/ms-go-notifications/app/types"
	"github.com/vibast-solutions/ms-go-notifications/config"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	_ "github.com/go-sql-driver/mysql"
	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP and gRPC servers",
	Long:  "Start both HTTP (Echo) and gRPC servers for the notifications service.",
	Run:   runServe,
}

// init registers the serve command.
func init() {
	rootCmd.AddCommand(serveCmd)
}

// runServe wires dependencies and starts HTTP and gRPC servers.
func runServe(_ *cobra.Command, _ []string) {
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
	producer := queue.NewEmailProducer(rdb)
	emailController := controller.NewEmailController(emailService, producer)
	grpcEmailServer := grpcserver.NewServer(emailService, producer)

	e := setupHTTPServer(cfg, emailController)
	grpcServer, lis := setupGRPCServer(cfg, grpcEmailServer)

	go func() {
		httpAddr := net.JoinHostPort(cfg.HTTPHost, cfg.HTTPPort)
		log.Printf("Starting HTTP server on %s", httpAddr)
		if err := e.Start(httpAddr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	go func() {
		log.Printf("Starting gRPC server on %s", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}
	grpcServer.GracefulStop()

	log.Println("Server stopped")
}

// setupHTTPServer configures the Echo HTTP server and routes.
func setupHTTPServer(cfg *config.Config, emailController *controller.EmailController) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(echomiddleware.Logger())
	e.Use(echomiddleware.Recover())
	e.Use(echomiddleware.CORS())

	email := e.Group("/email")
	email.POST("/send/raw", emailController.SendRaw)

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	return e
}

// setupGRPCServer builds the gRPC server and listener.
func setupGRPCServer(cfg *config.Config, emailServer *grpcserver.Server) (*grpc.Server, net.Listener) {
	grpcAddr := net.JoinHostPort(cfg.GRPCHost, cfg.GRPCPort)
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port: %v", err)
	}

	grpcServer := grpc.NewServer()
	types.RegisterNotificationsServiceServer(grpcServer, emailServer)

	return grpcServer, lis
}

func buildEmailProvider(cfg *config.Config) (provider.EmailProvider, error) {
	switch strings.ToLower(cfg.EmailProvider) {
	case "", "ses":
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(cfg.AWSRegion))
		if err != nil {
			return nil, err
		}
		return provider.NewSESProvider(awsCfg, cfg.SESSourceEmail), nil
	case "noop":
		return provider.NewNoopProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported EMAIL_PROVIDER: %s", cfg.EmailProvider)
	}
}
