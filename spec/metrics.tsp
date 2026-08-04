import "@typespec/http";

using Http;

namespace Farc;

@route("/metrics")
interface Metrics {
  @doc("""
    Prometheus text exposition (docs/docs/archive/02-storage.md §8's metric
    table), hand-rolled with no client-library dependency. One block of
    gauges/counters per currently-registered Storage, every line carrying a
    `storage="<id>"` label. Also reachable on MetricsEndpoint's own
    configured address (internal/api/server.go's MetricsHandler mounts the
    same handler a second time) — that second mount isn't a distinct
    operation, so it isn't modeled separately here.
    """)
  @get
  get(): {
    @header contentType: "text/plain; version=0.0.4";
    @body body: string;
  };
}
