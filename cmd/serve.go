package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	authclient "github.com/vibast-solutions/lib-go-auth/client"
	authmiddleware "github.com/vibast-solutions/lib-go-auth/middleware"
	authlibservice "github.com/vibast-solutions/lib-go-auth/service"
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
	"github.com/sirupsen/logrus"
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
	producer := queue.NewEmailProducer(rdb)
	emailController := controller.NewEmailController(emailService, producer)
	grpcEmailServer := grpcserver.NewServer(emailService, producer)

	authGRPCClient, err := authclient.NewGRPCClientFromAddr(context.Background(), cfg.InternalEndpoints.AuthGRPCAddr)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize auth gRPC client")
	}
	defer authGRPCClient.Close()
	internalAuthService := authlibservice.NewInternalAuthService(authGRPCClient)
	echoInternalAuthMiddleware := authmiddleware.NewEchoInternalAuthMiddleware(internalAuthService)
	grpcInternalAuthMiddleware := authmiddleware.NewGRPCInternalAuthMiddleware(internalAuthService)

	e := setupHTTPServer(emailController, echoInternalAuthMiddleware, cfg.App.ServiceName)
	grpcServer, lis := setupGRPCServer(cfg, grpcEmailServer, grpcInternalAuthMiddleware, cfg.App.ServiceName)

	go func() {
		httpAddr := net.JoinHostPort(cfg.HTTP.Host, cfg.HTTP.Port)
		logrus.WithField("addr", httpAddr).Info("Starting HTTP server")
		if err := e.Start(httpAddr); err != nil && err != http.ErrServerClosed {
			logrus.WithError(err).Fatal("HTTP server error")
		}
	}()

	go func() {
		logrus.WithField("addr", lis.Addr().String()).Info("Starting gRPC server")
		if err := grpcServer.Serve(lis); err != nil {
			logrus.WithError(err).Fatal("gRPC server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrus.Info("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		logrus.WithError(err).Warn("HTTP shutdown error")
	}
	grpcServer.GracefulStop()

	logrus.Info("Server stopped")
}

// setupHTTPServer configures the Echo HTTP server and routes.
func setupHTTPServer(
	emailController *controller.EmailController,
	internalAuthMiddleware *authmiddleware.EchoInternalAuthMiddleware,
	appServiceName string,
) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(echomiddleware.RequestLoggerWithConfig(echomiddleware.RequestLoggerConfig{
		LogURI:       true,
		LogStatus:    true,
		LogMethod:    true,
		LogRemoteIP:  true,
		LogLatency:   true,
		LogUserAgent: true,
		LogError:     true,
		HandleError:  true,
		LogValuesFunc: func(c echo.Context, v echomiddleware.RequestLoggerValues) error {
			fields := logrus.Fields{
				"remote_ip":  v.RemoteIP,
				"host":       v.Host,
				"method":     v.Method,
				"uri":        v.URI,
				"status":     v.Status,
				"latency":    v.Latency.String(),
				"latency_ns": v.Latency.Nanoseconds(),
				"user_agent": v.UserAgent,
			}
			entry := logrus.WithFields(fields)
			if v.Error != nil {
				entry = entry.WithError(v.Error)
			}
			entry.Info("http_request")
			return nil
		},
	}))
	e.Use(echomiddleware.Recover())
	e.Use(echomiddleware.CORS())
	e.Use(internalAuthMiddleware.RequireInternalAccess(appServiceName))

	email := e.Group("/email")
	email.POST("/send/raw", emailController.SendRaw)

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	return e
}

// setupGRPCServer builds the gRPC server and listener.
func setupGRPCServer(
	cfg *config.Config,
	emailServer *grpcserver.Server,
	internalAuthMiddleware *authmiddleware.GRPCInternalAuthMiddleware,
	appServiceName string,
) (*grpc.Server, net.Listener) {
	grpcAddr := net.JoinHostPort(cfg.GRPC.Host, cfg.GRPC.Port)
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to listen on gRPC port")
	}

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			internalAuthMiddleware.UnaryRequireInternalAccess(appServiceName),
		),
	)
	types.RegisterNotificationsServiceServer(grpcServer, emailServer)

	return grpcServer, lis
}

func buildEmailProvider(cfg *config.Config) (provider.EmailProvider, error) {
	switch strings.ToLower(cfg.EmailProviders.Provider) {
	case "", "ses":
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(cfg.EmailProviders.AWS.Region))
		if err != nil {
			return nil, err
		}
		return provider.NewSESProvider(awsCfg, cfg.EmailProviders.AWS.SourceEmail), nil
	case "noop":
		return provider.NewNoopProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported EMAIL_PROVIDER: %s", cfg.EmailProviders.Provider)
	}
}
