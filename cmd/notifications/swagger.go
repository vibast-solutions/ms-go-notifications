package main

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/vibast-solutions/ms-go-notifications/docs"
)

// swaggerIndexHTML is a minimal Swagger UI page that loads the generated spec
// from /swagger/doc.json. The UI assets come from the pinned swagger-ui-dist
// CDN build; the page is only served when SWAGGER_ENABLED is true (dev only).
const swaggerIndexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Vibast Notifications API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({ url: "/swagger/doc.json", dom_id: "#swagger-ui" });
    };
  </script>
</body>
</html>`

// registerSwagger serves the OpenAPI spec and Swagger UI under /swagger. It is
// wired only when SWAGGER_ENABLED is true.
func registerSwagger(e *echo.Echo) {
	e.GET("/swagger", func(c *echo.Context) error {
		return c.Redirect(http.StatusFound, "/swagger/index.html")
	})
	e.GET("/swagger/index.html", func(c *echo.Context) error {
		return c.HTML(http.StatusOK, swaggerIndexHTML)
	})
	e.GET("/swagger/doc.json", func(c *echo.Context) error {
		return c.Blob(http.StatusOK, echo.MIMEApplicationJSON, []byte(docs.SwaggerInfo.ReadDoc()))
	})
}
