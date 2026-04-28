package main

import (
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// promMetricsHandler returns the HTTP handler for /metrics, the Prometheus
// scrape endpoint exposing every histogram registered via internal/metrics.
//
// Access policy mirrors realtimeMetricsHandler (see comment there): if
// METRICS_TOKEN is set, callers must present it as Authorization: Bearer; if
// unset, only direct loopback callers can scrape — this keeps local dev
// frictionless while preventing accidental public exposure when the server
// sits behind a TLS-terminating reverse proxy on localhost.
func promMetricsHandler(token string) http.HandlerFunc {
	token = strings.TrimSpace(token)
	prom := promhttp.Handler()
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			if !hasBearerToken(r, token) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		} else if !isDirectLoopbackRequest(r) {
			http.NotFound(w, r)
			return
		}
		prom.ServeHTTP(w, r)
	}
}
