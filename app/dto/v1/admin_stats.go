package v1

import "time"

// StatCounts holds the per-bucket (and total) counters of the stats endpoint.
// accepted counts messages created in the bucket; sent/failed/suppressed are
// their current status; delivered/bounced/complained come from provider events
// that occurred in the bucket.
type StatCounts struct {
	Accepted    int `json:"accepted"`
	Sent        int `json:"sent"`
	Failed      int `json:"failed"`
	Suppressed  int `json:"suppressed"`
	Delivered   int `json:"delivered"`
	BouncedHard int `json:"bouncedHard"`
	BouncedSoft int `json:"bouncedSoft"`
	Complained  int `json:"complained"`
}

// StatBucket is one time bucket of the stats series. Bucket is the RFC3339 UTC
// start of the interval.
type StatBucket struct {
	Bucket string `json:"bucket"`
	StatCounts
}

// StatsBacklog reports the current queue depth (row counts), independent of the
// time window.
type StatsBacklog struct {
	Queued  int `json:"queued"`
	Sending int `json:"sending"`
}

// StatsResponse is the GET /v1/admin/stats payload: a zero-filled per-bucket
// series over [from, to), totals, the 24 h rolling bounce/complaint rates, the
// current sending (circuit-breaker) state and the live backlog gauges.
type StatsResponse struct {
	From          time.Time            `json:"from"`
	To            time.Time            `json:"to"`
	Granularity   string               `json:"granularity"`
	Buckets       []StatBucket         `json:"buckets"`
	Totals        StatCounts           `json:"totals"`
	BounceRate    float64              `json:"bounceRate"`
	ComplaintRate float64              `json:"complaintRate"`
	Sending       SendingStateResponse `json:"sending"`
	Backlog       StatsBacklog         `json:"backlog"`
}
