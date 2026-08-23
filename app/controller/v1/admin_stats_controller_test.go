package v1

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
	"github.com/vibast-solutions/ms-go-notifications/app/middleware"
	"github.com/vibast-solutions/ms-go-notifications/app/service"
)

type fakeAdminStats struct {
	resp *dtov1.StatsResponse
	err  error
}

func (f *fakeAdminStats) Stats(context.Context, service.StatsParams) (*dtov1.StatsResponse, error) {
	return f.resp, f.err
}

func newAdminStatsServer(svc AdminStats, key *entity.APIKey) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	auth := middleware.NewAuth(&fakeKeys{key: key})
	ctl := NewAdminStatsController(svc)
	e.Group("/v1/admin").GET("/stats", ctl.Get, auth.Require("admin"))
	return e
}

func TestAdminStatsHappy(t *testing.T) {
	svc := &fakeAdminStats{resp: &dtov1.StatsResponse{
		From:        time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC),
		To:          time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC),
		Granularity: "day",
		Buckets:     []dtov1.StatBucket{{Bucket: "2026-08-22T00:00:00Z", StatCounts: dtov1.StatCounts{Accepted: 3, Sent: 3}}},
		Totals:      dtov1.StatCounts{Accepted: 3, Sent: 3},
		Backlog:     dtov1.StatsBacklog{Queued: 1},
	}}
	e := newAdminStatsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/stats?granularity=day", "", true)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"granularity":"day"`)
	assert.Contains(t, rec.Body.String(), `"accepted":3`)
}

func TestAdminStatsValidationError(t *testing.T) {
	svc := &fakeAdminStats{err: service.NewValidationError(map[string]string{"granularity": "must be day or hour"})}
	e := newAdminStatsServer(svc, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/stats?granularity=week", "", true)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"validation_error"`)
}

func TestAdminStatsRequireAdminScope(t *testing.T) {
	e := newAdminStatsServer(&fakeAdminStats{}, sendKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/stats", "", true)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAdminStatsRequireAuth(t *testing.T) {
	e := newAdminStatsServer(&fakeAdminStats{}, adminKey())
	rec := doAdmin(e, http.MethodGet, "/v1/admin/stats", "", false)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
