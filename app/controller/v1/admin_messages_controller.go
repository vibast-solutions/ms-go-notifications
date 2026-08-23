package v1

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// AdminMessages is the slice of the admin service the messages controller
// needs. Implemented by service.AdminService.
type AdminMessages interface {
	ListMessages(ctx context.Context, p service.MessageListParams) (*dtov1.MessageListResponse, error)
	MessageDetail(ctx context.Context, id string) (*dtov1.AdminMessageResponse, error)
	Attachment(ctx context.Context, messageID, attachmentID string) (*entity.MessageAttachment, error)
	Retry(ctx context.Context, id, byName string) error
}

// AdminMessagesController serves the admin message endpoints under
// /v1/admin/messages. All routes require the admin scope.
type AdminMessagesController struct {
	svc AdminMessages
}

// NewAdminMessagesController returns a controller backed by the admin service.
func NewAdminMessagesController(svc AdminMessages) *AdminMessagesController {
	return &AdminMessagesController{svc: svc}
}

// List returns paginated message history.
//
//	@Summary		List messages
//	@Description	Paginated message history with filters. Cursor pagination on (createdAt, id) descending; pass the returned nextCursor to fetch the next page.
//	@Tags			admin
//	@Produce		json
//	@Param			status		query		string	false	"Filter by status (queued, sending, sent, failed, suppressed)"
//	@Param			fromDomain	query		string	false	"Filter by sender domain (exact)"
//	@Param			from		query		string	false	"Filter by sender email (contains)"
//	@Param			to			query		string	false	"Filter by recipient email (contains)"
//	@Param			subject		query		string	false	"Filter by subject (contains)"
//	@Param			apiKeyId	query		string	false	"Filter by owning API key id"
//	@Param			tag			query		string	false	"Filter by tag, formatted key:value"
//	@Param			createdFrom	query		string	false	"Only messages created at or after this time (RFC3339 or YYYY-MM-DD)"
//	@Param			createdTo	query		string	false	"Only messages created at or before this time (RFC3339 or YYYY-MM-DD)"
//	@Param			cursor		query		string	false	"Pagination cursor from a previous response"
//	@Param			limit		query		int		false	"Page size (1-100, default 25)"
//	@Success		200			{object}	dtov1.MessageListResponse
//	@Failure		400			{object}	dtov1.ErrorResponse	"validation_error"
//	@Failure		401			{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403			{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		500			{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/messages [get]
func (h *AdminMessagesController) List(c *echo.Context) error {
	p := service.MessageListParams{
		Status:      c.QueryParam("status"),
		FromDomain:  c.QueryParam("fromDomain"),
		From:        c.QueryParam("from"),
		To:          c.QueryParam("to"),
		Subject:     c.QueryParam("subject"),
		APIKeyID:    c.QueryParam("apiKeyId"),
		Tag:         c.QueryParam("tag"),
		CreatedFrom: c.QueryParam("createdFrom"),
		CreatedTo:   c.QueryParam("createdTo"),
		Cursor:      c.QueryParam("cursor"),
		Limit:       c.QueryParam("limit"),
	}
	resp, err := h.svc.ListMessages(c.Request().Context(), p)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, resp)
}

// Get returns the full admin detail for a message.
//
//	@Summary		Get message detail
//	@Description	Full message detail for debugging: the consumer view plus bodies, headers, attachment metadata, owning key, lock columns and full event payloads.
//	@Tags			admin
//	@Produce		json
//	@Param			id	path		string	true	"Message id (ULID)"
//	@Success		200	{object}	dtov1.AdminMessageResponse
//	@Failure		401	{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403	{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		404	{object}	dtov1.ErrorResponse	"not_found"
//	@Failure		500	{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/messages/{id} [get]
func (h *AdminMessagesController) Get(c *echo.Context) error {
	resp, err := h.svc.MessageDetail(c.Request().Context(), c.Param("id"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, resp)
}

// DownloadAttachment streams an attachment's bytes.
//
//	@Summary		Download an attachment
//	@Description	Streams the attachment content with its Content-Type and a Content-Disposition filename. Returns 410 gone once the content has been purged by retention.
//	@Tags			admin
//	@Produce		application/octet-stream
//	@Param			id				path	string	true	"Message id (ULID)"
//	@Param			attachmentId	path	string	true	"Attachment id (ULID)"
//	@Success		200				{file}	binary
//	@Failure		401				{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403				{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		404				{object}	dtov1.ErrorResponse	"not_found"
//	@Failure		410				{object}	dtov1.ErrorResponse	"gone"
//	@Failure		500				{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/messages/{id}/attachments/{attachmentId} [get]
func (h *AdminMessagesController) DownloadAttachment(c *echo.Context) error {
	att, err := h.svc.Attachment(c.Request().Context(), c.Param("id"), c.Param("attachmentId"))
	if err != nil {
		return err
	}
	if att.PurgedAt != nil || att.Content == nil {
		return echo.NewHTTPError(http.StatusGone, "attachment content has been purged")
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(att.Filename)+`"`)
	return c.Blob(http.StatusOK, att.ContentType, att.Content)
}

// Retry re-queues a failed message.
//
//	@Summary		Retry a failed message
//	@Description	Re-queues a message that is currently failed (status set to queued, re-dispatched). Returns 409 invalid_state when the message is in any other status.
//	@Tags			admin
//	@Produce		json
//	@Param			id	path	string	true	"Message id (ULID)"
//	@Success		202	{object}	nil
//	@Failure		401	{object}	dtov1.ErrorResponse	"unauthorized"
//	@Failure		403	{object}	dtov1.ErrorResponse	"forbidden"
//	@Failure		404	{object}	dtov1.ErrorResponse	"not_found"
//	@Failure		409	{object}	dtov1.ErrorResponse	"invalid_state"
//	@Failure		500	{object}	dtov1.ErrorResponse	"internal_error"
//	@Security		BearerAuth
//	@Router			/admin/messages/{id}/retry [post]
func (h *AdminMessagesController) Retry(c *echo.Context) error {
	if err := h.svc.Retry(c.Request().Context(), c.Param("id"), actorName(c)); err != nil {
		return err
	}
	return c.NoContent(http.StatusAccepted)
}

// sanitizeFilename strips characters that would break the Content-Disposition
// header (quotes, backslashes, control characters), leaving a safe ASCII name.
func sanitizeFilename(name string) string {
	replaced := strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if replaced == "" {
		return "attachment"
	}
	return replaced
}
