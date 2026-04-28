// Package metrics centralises Prometheus instruments shared across handlers.
//
// We bias toward small numbers of well-named, hand-curated histograms here
// rather than auto-generated request metrics — the goal is to give SRE
// (JARVIS) targets they can build alerts against without sifting through
// thousands of synthetic series. The full Prometheus default registry is
// exposed via cmd/server/router.go on /metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CrossWorkspaceIssuesDuration tracks GET /api/issues/cross-workspace latency
// in milliseconds. The endpoint joins three tables and is the hot path for the
// global Kanban view, so we want a native histogram instead of a derived
// average. Buckets cover the 1ms…2s range observed in load tests.
var CrossWorkspaceIssuesDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "multica_cross_workspace_issues_duration_ms",
	Help:    "Latency of GET /api/issues/cross-workspace in milliseconds.",
	Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2000},
})
