package main

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	echomw "github.com/labstack/echo/v5/middleware"
	log "github.com/sirupsen/logrus"

	controllerv1 "github.com/vibast-solutions/ms-go-notifications/app/controller/v1"
	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	appmw "github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/repository"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
	"github.com/vibast-solutions/ms-go-notifications/config"
	"github.com/vibast-solutions/ms-go-notifications/pkg/sns"
)

// snsHTTPTimeout bounds the outbound calls the SNS webhook makes: the signing
// certificate download and the subscription-confirmation GET (Business Logic §7
// / API v1: 10 s).
const snsHTTPTimeout = 10 * time.Second

// newServer wires the Echo instance: global middleware, /healthz and the /v1
// group with bearer auth. Routes opt into scopes with auth.Require(<scope>). The
// dispatcher is the in-process worker pool (or a no-op in --no-worker mode); the
// accept path hands committed message ids to it.
func newServer(cfg *config.Config, db *sql.DB, dispatcher service.Dispatcher, reputation *service.ReputationService) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = appmw.ErrorHandler

	e.Use(appmw.RequestID())
	// Accept the configured payload plus 20% headroom for JSON/base64 framing.
	e.Use(echomw.BodyLimit(int64(cfg.MaxPayloadBytes) / 5 * 6))

	keys := service.NewAPIKeyService(repository.NewAPIKeyRepository(db), cfg.AllowedDomains)
	auth := appmw.NewAuth(keys)

	emails := service.NewEmailService(
		repository.NewMessageRepository(db),
		repository.NewSuppressionRepository(db),
		dispatcher,
		dtov1.Limits{MaxRecipients: cfg.MaxRecipients, MaxPayloadBytes: cfg.MaxPayloadBytes},
	)

	health := controllerv1.NewHealthController(db, reputation)
	e.GET("/healthz", health.Healthz)

	v1 := e.Group("/v1")
	v1.Use(appmw.APIVersion("1"))

	emailsCtl := controllerv1.NewEmailsController(emails)
	v1.POST("/emails", emailsCtl.Send, auth.Require("send"))
	v1.GET("/emails/:id", emailsCtl.Get, auth.Require("send"))

	whoami := controllerv1.NewWhoamiController()
	// Any valid key may call whoami (empty scope = authentication only).
	v1.GET("/whoami", whoami.Whoami, auth.Require(""))

	// Admin group (scope admin). VIB-11 adds the reputation circuit-breaker
	// endpoints; VIB-12 extends this group with messages/stats/suppressions/keys.
	admin := v1.Group("/admin")
	requireAdmin := auth.Require("admin")

	sendingCtl := controllerv1.NewAdminSendingController(reputation)
	admin.GET("/sending", sendingCtl.Get, requireAdmin)
	admin.POST("/sending/pause", sendingCtl.Pause, requireAdmin)
	admin.POST("/sending/resume", sendingCtl.Resume, requireAdmin)

	messageRepo := repository.NewMessageRepository(db)
	suppressionRepo := repository.NewSuppressionRepository(db)
	adminSvc := service.NewAdminService(messageRepo, suppressionRepo, keys, reputation, dispatcher)

	messagesCtl := controllerv1.NewAdminMessagesController(adminSvc)
	admin.GET("/messages", messagesCtl.List, requireAdmin)
	admin.GET("/messages/:id", messagesCtl.Get, requireAdmin)
	admin.GET("/messages/:id/attachments/:attachmentId", messagesCtl.DownloadAttachment, requireAdmin)
	admin.POST("/messages/:id/retry", messagesCtl.Retry, requireAdmin)

	statsCtl := controllerv1.NewAdminStatsController(adminSvc)
	admin.GET("/stats", statsCtl.Get, requireAdmin)

	suppressionsCtl := controllerv1.NewAdminSuppressionsController(adminSvc)
	admin.GET("/suppressions", suppressionsCtl.List, requireAdmin)
	admin.POST("/suppressions", suppressionsCtl.Create, requireAdmin)
	admin.DELETE("/suppressions/:email", suppressionsCtl.Delete, requireAdmin)

	keysCtl := controllerv1.NewAdminKeysController(keys)
	admin.GET("/api-keys", keysCtl.List, requireAdmin)
	admin.POST("/api-keys", keysCtl.Create, requireAdmin)
	admin.PUT("/api-keys/:id/domains", keysCtl.ReplaceDomains, requireAdmin)
	admin.DELETE("/api-keys/:id", keysCtl.Revoke, requireAdmin)

	// SES event webhook (SNS). No bearer auth — the SNS signature and the topic
	// allow-list are the credential. Wired here (not in the worker) so ingestion
	// works even in --no-worker mode.
	events := service.NewEventService(
		repository.NewMessageRepository(db),
		repository.NewEventRepository(db),
		repository.NewSuppressionRepository(db),
		log.StandardLogger(),
	)
	verifier := sns.NewVerifier(&http.Client{Timeout: snsHTTPTimeout})
	topics := sns.NewTopicAllowList(cfg.SNSAllowedTopicARNs)
	webhooks := controllerv1.NewWebhooksController(verifier, topics, events, log.StandardLogger())
	v1.POST("/webhooks/ses", webhooks.SES)

	if cfg.SwaggerEnabled {
		registerSwagger(e)
	}

	return e
}
