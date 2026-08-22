package middleware

import "github.com/labstack/echo/v5"

// APIVersionHeader announces the API version of the group serving the request.
const APIVersionHeader = "X-API-Version"

// APIVersion stamps every response of a version group with X-API-Version.
// Deprecation/Sunset headers for retiring versions are added when a sunset
// date is configured (API_V1_SUNSET; wired in a later story).
func APIVersion(version string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			c.Response().Header().Set(APIVersionHeader, version)
			return next(c)
		}
	}
}
