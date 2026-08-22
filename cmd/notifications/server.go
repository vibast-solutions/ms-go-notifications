package main

import (
	"database/sql"
	"net/http"

	"github.com/labstack/echo/v5"
	echomw "github.com/labstack/echo/v5/middleware"

	controllerv1 "github.com/vibast-solutions/ms-go-notifications/app/controller/v1"
	appmw "github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	"github.com/vibast-solutions/ms-go-notifications/config"
)

// newServer wires the Echo instance: global middleware, /healthz and the /v1
// group with bearer auth. Later stories register their routes here with
// auth.Require(<scope>), e.g. v1.POST("/emails", h, auth.Require("send")).
func newServer(cfg *config.Config, db *sql.DB) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = appmw.ErrorHandler

	e.Use(appmw.RequestID())
	// Accept the configured payload plus 20% headroom for JSON/base64 framing.
	e.Use(echomw.BodyLimit(int64(cfg.MaxPayloadBytes) / 5 * 6))

	keys := service.NewAPIKeyService(repository.NewAPIKeyRepository(db), cfg.AllowedDomains)
	auth := appmw.NewAuth(keys)

	health := controllerv1.NewHealthController(db)
	e.GET("/healthz", health.Healthz)

	v1 := e.Group("/v1")
	v1.Use(appmw.APIVersion("1"))
	// Placeholder until VIB-8 adds /v1/emails.
	v1.GET("/ping", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	whoami := controllerv1.NewWhoamiController()
	// Any valid key may call whoami (empty scope = authentication only).
	v1.GET("/whoami", whoami.Whoami, auth.Require(""))

	return e
}
