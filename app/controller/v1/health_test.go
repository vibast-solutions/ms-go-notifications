package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePinger struct {
	err error
}

func (f fakePinger) PingContext(context.Context) error {
	return f.err
}

func performHealthz(t *testing.T, pinger DBPinger) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, NewHealthController(pinger).Healthz(c))
	return rec
}

func TestHealthzOK(t *testing.T) {
	rec := performHealthz(t, fakePinger{})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok","db":"ok"}`, rec.Body.String())
}

func TestHealthzDBDown(t *testing.T) {
	rec := performHealthz(t, fakePinger{err: errors.New("connection refused")})

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"status":"error","db":"error"}`, rec.Body.String())
}
