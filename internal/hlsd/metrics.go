package hlsd

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// connectedChannelsDesc is hls_server's one domain gauge (.scratch/
// observability/spec.md): the count of channels currently tracked/served,
// reported alongside client_golang's free go_*/process_* runtime metrics.
var connectedChannelsDesc = prometheus.NewDesc("hls_server_connected_channels", "Number of channels currently tracked/served.", nil, nil)

// hlsdCollector reports h.ConnectedChannels() at scrape time.
type hlsdCollector struct {
	h *Hlsd
}

func (c *hlsdCollector) Describe(ch chan<- *prometheus.Desc) { ch <- connectedChannelsDesc }

func (c *hlsdCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(connectedChannelsDesc, prometheus.GaugeValue, float64(c.h.ConnectedChannels()))
}

// newMetricsRegistry builds h's metrics registry: client_golang's Go/process
// runtime collectors plus hlsdCollector. Split out from the /metrics handler
// itself (below) so tracing.HTTPMetrics can register onto this same
// registry -- registering HTTP request metrics on a separate registry would
// make them silently unreachable on scrape.
func newMetricsRegistry(h *Hlsd) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		&hlsdCollector{h: h},
	)
	return reg
}

// newMetricsHandler serves reg (built by newMetricsRegistry) as /metrics.
func newMetricsHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
