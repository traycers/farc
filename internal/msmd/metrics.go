package msmd

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// wsConnectedDesc is msm_server's one domain gauge (.scratch/observability/
// spec.md): whether the outbound WS subscription to farcd's event feed is
// currently connected (1) or not (0), reported alongside client_golang's
// free go_*/process_* runtime metrics.
var wsConnectedDesc = prometheus.NewDesc("msm_server_ws_connected", "Whether msm_server's WS subscription to farcd's event feed is currently connected.", nil, nil)

// wsConnectedCollector reports connected's current value at scrape time.
type wsConnectedCollector struct {
	connected *atomic.Bool
}

func (c *wsConnectedCollector) Describe(ch chan<- *prometheus.Desc) { ch <- wsConnectedDesc }

func (c *wsConnectedCollector) Collect(ch chan<- prometheus.Metric) {
	v := 0.0
	if c.connected.Load() {
		v = 1
	}
	ch <- prometheus.MustNewConstMetric(wsConnectedDesc, prometheus.GaugeValue, v)
}

// newMetricsHandler builds msm_server's /metrics handler: client_golang's
// Go/process runtime collectors plus wsConnectedCollector.
func newMetricsHandler(connected *atomic.Bool) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		&wsConnectedCollector{connected: connected},
	)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
