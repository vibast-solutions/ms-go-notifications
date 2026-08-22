// Package middleware holds the HTTP middleware shared by all API versions:
// request id, the JSON error handler, the X-API-Version header, and bearer
// API key auth with scopes (auth.go).
package middleware

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
	log "github.com/sirupsen/logrus"

	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

// errorBody is the standard error envelope returned to API clients:
// a machine-readable code, a human message, and optional field errors.
type errorBody struct {
	Error   string            `json:"error"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// ErrorHandler is the global Echo error handler producing the
// {error, message, fields} envelope. *echo.HTTPError keeps its status code
// and gets a semantic code derived from it; anything else becomes a 500 with
// a generic message — internal error details are never exposed to clients.
// 500s are logged with the request id, method and path.
func ErrorHandler(c *echo.Context, err error) {
	resp, _ := echo.UnwrapResponse(c.Response())
	if resp != nil && resp.Committed {
		return
	}

	// Domain errors from the service layer carry their own semantic code (and,
	// for validation, field details) so the envelope stays meaningful.
	if handleServiceError(c, err) {
		return
	}

	code := http.StatusInternalServerError
	body := errorBody{
		Error:   "internal_error",
		Message: "An unexpected error occurred",
	}

	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
		body.Message = he.Message
		switch code {
		case http.StatusBadRequest:
			body.Error = "bad_request"
		case http.StatusUnauthorized:
			body.Error = "unauthorized"
		case http.StatusForbidden:
			body.Error = "forbidden"
		case http.StatusNotFound:
			body.Error = "not_found"
		case http.StatusMethodNotAllowed:
			body.Error = "method_not_allowed"
		case http.StatusRequestEntityTooLarge:
			body.Error = "payload_too_large"
		case http.StatusTooManyRequests:
			body.Error = "rate_limit_exceeded"
		default:
			body.Error = "http_error"
		}
	}

	if code == http.StatusInternalServerError {
		reqID, _ := c.Get(RequestIDContextKey).(string)
		log.WithFields(log.Fields{
			"request_id": reqID,
			"method":     c.Request().Method,
			"path":       c.Request().URL.Path,
			"error":      err.Error(),
		}).Error("unhandled error")
	}

	_ = c.JSON(code, body)
}

// handleServiceError writes the envelope for a known service domain error and
// reports whether it handled err. Unknown errors are left to the caller.
func handleServiceError(c *echo.Context, err error) bool {
	var ve *service.ValidationError
	switch {
	case errors.As(err, &ve):
		_ = c.JSON(http.StatusBadRequest, errorBody{
			Error:   "validation_error",
			Message: "one or more fields are invalid",
			Fields:  ve.Fields,
		})
	case errors.Is(err, service.ErrDomainNotAllowed):
		_ = c.JSON(http.StatusForbidden, errorBody{
			Error:   "domain_not_allowed",
			Message: "the sender domain is not allowed for this API key",
		})
	case errors.Is(err, service.ErrPayloadTooLarge):
		_ = c.JSON(http.StatusRequestEntityTooLarge, errorBody{
			Error:   "payload_too_large",
			Message: "the decoded payload exceeds the maximum allowed size",
		})
	case errors.Is(err, service.ErrNotFound):
		_ = c.JSON(http.StatusNotFound, errorBody{
			Error:   "not_found",
			Message: "the requested resource was not found",
		})
	default:
		return false
	}
	return true
}
