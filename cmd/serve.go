package cmd

import (
	"log"
	"net"

	"github.com/vibast-solutions/ms-go-notifications/config"

	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP and gRPC servers",
	Long:  "Start both HTTP (Echo) and gRPC servers for the notifications service.",
	Run:   runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(_ *cobra.Command, _ []string) {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	go startGRPCServer(cfg)
	startHTTPServer(cfg)
}

func startHTTPServer(cfg *config.Config) {
	e := echo.New()
	defer e.Close()
	e.HideBanner = true

	e.Use(echomiddleware.Logger())
	e.Use(echomiddleware.Recover())
	e.Use(echomiddleware.CORS())

	e.GET("/health", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"status": "ok"})
	})

	httpAddr := net.JoinHostPort(cfg.HTTPHost, cfg.HTTPPort)
	log.Printf("Starting HTTP server on %s", httpAddr)
	if err := e.Start(httpAddr); err != nil {
		log.Fatalf("Failed to start HTTP server: %v", err)
	}
}

func startGRPCServer(cfg *config.Config) {
	grpcAddr := net.JoinHostPort(cfg.GRPCHost, cfg.GRPCPort)
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port: %v", err)
	}

	grpcServer := grpc.NewServer()
	defer grpcServer.GracefulStop()

	log.Printf("Starting gRPC server on %s", grpcAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to start gRPC server: %v", err)
	}
}
