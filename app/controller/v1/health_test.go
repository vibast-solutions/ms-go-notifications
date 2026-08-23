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

// fakeSending is a SendingStateReader with a fixed paused flag and optional
// error.
type fakeSending struct {
	paused bool
	err    error
}

func (f fakeSending) SendingPaused(context.Context) (bool, error) {
	return f.paused, f.err
}

func performHealthz(t *testing.T, pinger DBPinger, sending SendingStateReader) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, NewHealthController(pinger, sending).Healthz(c))
	return rec
}

func TestHealthzOK(t *testing.T) {
	rec := performHealthz(t, fakePinger{}, fakeSending{})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok","db":"ok","sendingPaused":false}`, rec.Body.String())
}

func TestHealthzReportsSendingPaused(t *testing.T) {
	rec := performHealthz(t, fakePinger{}, fakeSending{paused: true})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok","db":"ok","sendingPaused":true}`, rec.Body.String())
}

func TestHealthzSendingReadErrorDefaultsFalse(t *testing.T) {
	rec := performHealthz(t, fakePinger{}, fakeSending{err: errors.New("db blip")})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok","db":"ok","sendingPaused":false}`, rec.Body.String())
}

func TestHealthzDBDown(t *testing.T) {
	rec := performHealthz(t, fakePinger{err: errors.New("connection refused")}, fakeSending{})

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.JSONEq(t, `{"status":"error","db":"error"}`, rec.Body.String())
}
