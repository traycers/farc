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

// newMetricsHandler builds h's /metrics handler: client_golang's Go/process
// runtime collectors plus hlsdCollector.
func newMetricsHandler(h *Hlsd) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		&hlsdCollector{h: h},
	)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
