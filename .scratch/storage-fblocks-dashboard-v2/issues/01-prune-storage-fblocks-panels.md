# 01 — Prune Storage & Fblocks dashboard down to write-verify only

Status: open

## Task

`deploy/observability/grafana/dashboards/storage-fblocks.json` has 5
panels. Remove 4 of them, keeping only the write-verify one (see spec
decision 2 for why exactly these 4 and not the rest).

1. Delete the panel objects with `"id": 1` ("Fblock states by storage"),
   `"id": 2` ("Write queue depth"), `"id": 3` ("Write queue status
   (0=normal, 1=warning, 2=backpressure)"), and `"id": 5` ("Channel
   registry usage") from the top-level `panels` array.
2. Leave `"id": 4` ("Write-verify failures / sec",
   `rate(farc_write_verify_failures_total[5m])`) completely untouched.
3. Don't try to re-lay-out the freed grid space in this issue — issues
   02-04 each place their own new panel with its own `gridPos`.

## Tests

No test suite covers dashboard JSON in this repo — this is a manual/visual
change, verified by inspection.

## Verify

`deploy/observability/grafana/provisioning/dashboards/dashboards.yaml` sets
`updateIntervalSeconds: 30`, so Grafana's file provisioner picks up the
edited JSON automatically within ~30s — no restart needed. Open Grafana →
`farc` folder → "Storage & Fblocks" and confirm only "Write-verify
failures / sec" remains (until issues 02-04 add their panels).
