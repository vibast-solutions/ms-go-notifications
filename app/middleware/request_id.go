package middleware

import (
	"github.com/labstack/echo/v5"
	"github.com/oklog/ulid/v2"
)

// RequestIDHeader propagates request ids between client and server. A client
// value is reused (distributed tracing); otherwise a ULID is generated.
const RequestIDHeader = "X-Request-Id"

// RequestIDContextKey is the Echo context key the resolved id is stored under.
const RequestIDContextKey = "request_id"

// RequestID assigns every request an id, stores it in the context and echoes
// it back in the response header.
func RequestID() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			id := c.Request().Header.Get(RequestIDHeader)
			if id == "" {
				id = ulid.Make().String()
			}
			c.Set(RequestIDContextKey, id)
			c.Response().Header().Set(RequestIDHeader, id)
			return next(c)
		}
	}
}
