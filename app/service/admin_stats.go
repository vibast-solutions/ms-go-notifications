package service

import (
	"context"
	"fmt"
	"time"

	dtov1 "github.com/vibast-solutions/ms-go-notifications/app/dto/v1"
	"github.com/vibast-solutions/ms-go-notifications/app/entity"
)

// StatsParams are the raw GET /v1/admin/stats query values.
type StatsParams struct {
	From        string
	To          string
	FromDomain  string
	Granularity string
}

// Stats computes the dashboard numbers: a zero-filled per-bucket series over
// the requested window, totals, the 24 h rolling bounce/complaint rates, the
// current sending state and the live backlog. Windows are UTC.
func (s *AdminService) Stats(ctx context.Context, p StatsParams) (*dtov1.StatsResponse, error) {
	fields := map[string]string{}

	granularity := p.Granularity
	if granularity == "" {
		granularity = "day"
	}
	if granularity != "day" && granularity != "hour" {
		fields["granularity"] = "must be day or hour"
	}

	now := s.now().UTC()
	to := now
	if t := parseTimeField(p.To, "to", fields); t != nil {
		to = *t
	}
	from := to.Add(-defaultStatsDays * 24 * time.Hour)
	if t := parseTimeField(p.From, "from", fields); t != nil {
		from = *t
	}
	if !from.Before(to) {
		fields["from"] = "must be before to"
	}

	// Snap the window start to a bucket boundary so buckets align, and reject a
	// range that would produce an unreasonable number of buckets.
	var start time.Time
	if _, ok := fields["granularity"]; !ok {
		if _, badFrom := fields["from"]; !badFrom {
			start = truncateToBucket(from, granularity)
			if n := countBuckets(start, to, granularity); n > maxStatsBuckets {
				fields["granularity"] = fmt.Sprintf("range too wide for %s granularity (max %d buckets)", granularity, maxStatsBuckets)
			}
		}
	}

	if len(fields) > 0 {
		return nil, NewValidationError(fields)
	}

	fromDomain := lowerTrim(p.FromDomain)

	// Build the ordered, zero-filled bucket series and an index for merging.
	resp := &dtov1.StatsResponse{
		From:        start,
		To:          to,
		Granularity: granularity,
		Buckets:     []dtov1.StatBucket{},
	}
	index := map[string]int{}
	for b := start; b.Before(to); b = stepBucket(b, granularity) {
		key := bucketKey(b, granularity)
		index[key] = len(resp.Buckets)
		resp.Buckets = append(resp.Buckets, dtov1.StatBucket{Bucket: key})
	}

	// Messages: accepted / sent / failed / suppressed, bucketed by created_at.
	msgRows, err := s.messages.StatsMessageBuckets(ctx, start, to, fromDomain, granularity)
	if err != nil {
		return nil, fmt.Errorf("message stats: %w", err)
	}
	for _, row := range msgRows {
		i, ok := index[row.Bucket]
		if !ok {
			continue
		}
		b := &resp.Buckets[i]
		b.Accepted += row.Count
		switch row.Status {
		case entity.MessageStatusSent:
			b.Sent += row.Count
		case entity.MessageStatusFailed:
			b.Failed += row.Count
		case entity.MessageStatusSuppressed:
			b.Suppressed += row.Count
		}
	}

	// Events: delivered / bounced (hard,soft) / complained, bucketed by
	// occurred_at.
	evRows, err := s.messages.StatsEventBuckets(ctx, start, to, fromDomain, granularity)
	if err != nil {
		return nil, fmt.Errorf("event stats: %w", err)
	}
	for _, row := range evRows {
		i, ok := index[row.Bucket]
		if !ok {
			continue
		}
		b := &resp.Buckets[i]
		b.Delivered += row.Delivered
		b.BouncedHard += row.BouncedHard
		b.BouncedSoft += row.BouncedSoft
		b.Complained += row.Complained
	}

	// Totals across the series.
	for i := range resp.Buckets {
		c := resp.Buckets[i].StatCounts
		resp.Totals.Accepted += c.Accepted
		resp.Totals.Sent += c.Sent
		resp.Totals.Failed += c.Failed
		resp.Totals.Suppressed += c.Suppressed
		resp.Totals.Delivered += c.Delivered
		resp.Totals.BouncedHard += c.BouncedHard
		resp.Totals.BouncedSoft += c.BouncedSoft
		resp.Totals.Complained += c.Complained
	}

	// 24 h rolling reputation rates.
	_, _, _, bounceRate, complaintRate, err := s.reputation.ComputeRates(ctx, ReputationWindow)
	if err != nil {
		return nil, fmt.Errorf("computing rates: %w", err)
	}
	resp.BounceRate = bounceRate
	resp.ComplaintRate = complaintRate

	// Current sending (circuit-breaker) state.
	state, err := s.reputation.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading sending state: %w", err)
	}
	resp.Sending = dtov1.SendingStateResponse{Paused: state.Paused, Reason: state.Reason, By: state.By}
	if state.Paused && !state.Since.IsZero() {
		since := state.Since
		resp.Sending.Since = &since
	}

	// Live backlog gauges.
	counts, err := s.messages.CountMessagesByStatus(ctx, []string{entity.MessageStatusQueued, entity.MessageStatusSending})
	if err != nil {
		return nil, fmt.Errorf("counting backlog: %w", err)
	}
	resp.Backlog = dtov1.StatsBacklog{
		Queued:  counts[entity.MessageStatusQueued],
		Sending: counts[entity.MessageStatusSending],
	}

	return resp, nil
}

// truncateToBucket rounds t down to the start of its day or hour (UTC).
func truncateToBucket(t time.Time, granularity string) time.Time {
	t = t.UTC()
	if granularity == "hour" {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// stepBucket advances one day or hour.
func stepBucket(t time.Time, granularity string) time.Time {
	if granularity == "hour" {
		return t.Add(time.Hour)
	}
	return t.AddDate(0, 0, 1)
}

// bucketKey formats a bucket start as an RFC3339 UTC key matching the SQL
// DATE_FORMAT output.
func bucketKey(t time.Time, granularity string) string {
	if granularity == "hour" {
		return t.UTC().Format("2006-01-02T15:00:00Z")
	}
	return t.UTC().Format("2006-01-02T00:00:00Z")
}

// countBuckets returns how many buckets [start, to) spans at the granularity.
func countBuckets(start, to time.Time, granularity string) int {
	n := 0
	for b := start; b.Before(to); b = stepBucket(b, granularity) {
		n++
		if n > maxStatsBuckets {
			break
		}
	}
	return n
}

// lowerTrim trims and lower-cases a value (fromDomain normalization).
func lowerTrim(s string) string { return dtov1.NormalizeEmail(s) }
