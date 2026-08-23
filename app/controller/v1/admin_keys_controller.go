package v1

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// AdminKeys is the slice of the API key service the admin keys controller
// needs. Implemented by service.APIKeyService.
type AdminKeys interface {
	Generate(ctx context.Context, name string, scopes, domains []string) (string, *entity.APIKey, error)
	List(ctx context.Context) ([]*entity.APIKey, error)
	Get(ctx context.Context, id string) (*entity.APIKey, error)
	ReplaceDomains(ctx context.Context, id string, domains []string) error
	Revoke(ctx context.Context, id string) error
}

// AdminKeysController serves the API key management endpoints under
// /v1/admin/api-keys. All routes require the admin scope.
type AdminKeysController struct {
	keys AdminKeys
}

// NewAdminKeysController returns a controller backed by the API key service.
func NewAdminKeysController(keys AdminKeys) *AdminKeysController {
	return &AdminKeysController{keys: keys}
}

// List returns all API keys (never the secret).
//
//	@Summary		List API keys
//	@Description	Lists every API key with its scopes, domains and status. The secret is never returned.
//	@Tags			admin
//	@Produce		json
//	@Success		200	{object}	dtov1.APIKeyListResponse
//	@Failure		401	{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403	{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500	{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/api-keys [get]
func (h *AdminKeysController) List(c *echo.Context) error {
	keys, err := h.keys.List(c.Request().Context())
	if err != nil {
		return err
	}
	resp := dtov1.APIKeyListResponse{Items: make([]dtov1.APIKeyResponse, 0, len(keys))}
	for _, k := range keys {
		resp.Items = append(resp.Items, dtov1.NewAPIKeyResponse(k))
	}
	return c.JSON(http.StatusOK, resp)
}

// Create mints a new API key and returns its plaintext once.
//
//	@Summary		Create an API key
//	@Description	Creates an API key. domains are required (≥1) when scopes include "send" and must each be in ALLOWED_DOMAINS. The plaintext key is returned exactly once.
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Param			request	body		dtov1.CreateAPIKeyRequest	true	"Key to create"
//	@Success		201		{object}	dtov1.CreateAPIKeyResponse
//	@Failure		400		{object}	dtov1.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403		{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500		{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/api-keys [post]
func (h *AdminKeysController) Create(c *echo.Context) error {
	var req dtov1.CreateAPIKeyRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	plaintext, key, err := h.keys.Generate(c.Request().Context(), req.Name, req.Scopes, req.Domains)
	if err != nil {
		return err
	}
	resp := dtov1.CreateAPIKeyResponse{APIKeyResponse: dtov1.NewAPIKeyResponse(key), Key: plaintext}
	return c.JSON(http.StatusCreated, resp)
}

// ReplaceDomains replaces a key's sender domains.
//
//	@Summary		Replace a key's domains
//	@Description	Replaces the sender domain list of a key (≥1, each in ALLOWED_DOMAINS for a send key).
//	@Tags			admin
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"API key id"
//	@Param			request	body		dtov1.ReplaceDomainsRequest	true	"New domain list"
//	@Success		200		{object}	dtov1.APIKeyResponse
//	@Failure		400		{object}	dtov1.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403		{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		404		{object}	dtov1.ErrorResponse	"not_found"
//	@Failure		500		{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/api-keys/{id}/domains [put]
func (h *AdminKeysController) ReplaceDomains(c *echo.Context) error {
	var req dtov1.ReplaceDomainsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	id := c.Param("id")
	if err := h.keys.ReplaceDomains(ctx, id, req.Domains); err != nil {
		return err
	}
	key, err := h.keys.Get(ctx, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, dtov1.NewAPIKeyResponse(key))
}

// Revoke revokes an API key.
//
//	@Summary		Revoke an API key
//	@Description	Revokes a key. A key cannot revoke itself (400 invalid_request).
//	@Tags			admin
//	@Produce		json
//	@Param			id	path	string	true	"API key id"
//	@Success		204	{object}	nil
//	@Failure		400	{object}	dtov1.ErrorResponse	"invalid_request"
//	@Failure		401	{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403	{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		404	{object}	dtov1.ErrorResponse	"not_found"
//	@Failure		500	{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/api-keys/{id} [delete]
func (h *AdminKeysController) Revoke(c *echo.Context) error {
	id := c.Param("id")
	if caller := middleware.GetAPIKey(c); caller != nil && caller.ID == id {
		return service.ErrSelfRevoke
	}
	if err := h.keys.Revoke(c.Request().Context(), id); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
