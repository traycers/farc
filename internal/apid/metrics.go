package apid

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// newMetricsRegistry builds apid's metrics registry: just client_golang's
// Go/process runtime collectors for now -- apid has no domain gauge yet
// (unlike hlsd's hlsd_connected_channels,
// internal/hlsd/metrics.go), since nothing here has needed one so far.
// Exposed separately from the /metrics handler itself so
// tracing.HTTPMetrics can register HTTP request metrics onto this same
// registry -- a separate registry would make them silently unreachable on
// scrape.
func newMetricsRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

func newMetricsHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
