package main

import (
	"database/sql"

	"github.com/labstack/echo/v5"
	echomw "github.com/labstack/echo/v5/middleware"

	controllerv1 "github.com/vibast-solutions/ms-go-notifications/app/controller/v1"
	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	appmw "github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	"github.com/vibast-solutions/ms-go-notifications/config"
	"github.com/vibast-solutions/ms-go-notifications/internal/worker"
)

// newServer wires the Echo instance: global middleware, /healthz and the /v1
// group with bearer auth. Routes opt into scopes with auth.Require(<scope>).
func newServer(cfg *config.Config, db *sql.DB) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = appmw.ErrorHandler

	e.Use(appmw.RequestID())
	// Accept the configured payload plus 20% headroom for JSON/base64 framing.
	e.Use(echomw.BodyLimit(int64(cfg.MaxPayloadBytes) / 5 * 6))

	keys := service.NewAPIKeyService(repository.NewAPIKeyRepository(db), cfg.AllowedDomains)
	auth := appmw.NewAuth(keys)

	// The dispatcher is a no-op until VIB-9 wires the real worker pool; the
	// accept path still stores messages and the sweeper will drain them.
	emails := service.NewEmailService(
		repository.NewMessageRepository(db),
		repository.NewSuppressionRepository(db),
		worker.NoopDispatcher{},
		dtov1.Limits{MaxRecipients: cfg.MaxRecipients, MaxPayloadBytes: cfg.MaxPayloadBytes},
	)

	health := controllerv1.NewHealthController(db)
	e.GET("/healthz", health.Healthz)

	v1 := e.Group("/v1")
	v1.Use(appmw.APIVersion("1"))

	emailsCtl := controllerv1.NewEmailsController(emails)
	v1.POST("/emails", emailsCtl.Send, auth.Require("send"))
	v1.GET("/emails/:id", emailsCtl.Get, auth.Require("send"))

	whoami := controllerv1.NewWhoamiController()
	// Any valid key may call whoami (empty scope = authentication only).
	v1.GET("/whoami", whoami.Whoami, auth.Require(""))

	if cfg.SwaggerEnabled {
		registerSwagger(e)
	}

	return e
}
