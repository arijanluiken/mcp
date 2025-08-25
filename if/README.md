# Isolation Forest Anomaly Detector (if-service)

This service detects anomalies on OpenTelemetry spanmetrics using an Isolation Forest. It queries a Prometheus-compatible backend (Grafana Mimir) for request-rate and error-rate time series, scores each series independently, and returns/logs the most anomalous points.

## What it does
- Pulls spanmetrics from Mimir (via PromQL) at 1-minute resolution over a configurable window.
- Computes per-series anomalies with a 1D Isolation Forest.
- Exposes HTTP endpoints to list top anomalies for:
  - All server spans by service/span/peer on request rate (RPS)
  - All server spans by service/span/peer on error rate (errors/total)
- Emits an event to stdout when a detected anomaly crosses a score threshold.

## Data source and labels
- Assumes an OpenTelemetry Collector exports spanmetrics to Mimir.
- This repo’s `otel-collector-config.yaml` uses the `spanmetrics` connector with an extra dimension:
  - `peer.service` is added (so you can see caller -> callee).
- Series are grouped by: `service_name`, `span_name`, `peer_service`.
- Only server spans are considered for RPS and error-rate (span_kind="SPAN_KIND_SERVER").

Supported metric name variants (auto-detected):
- `traces_spanmetrics_calls_total`
- `traces_span_metrics_calls_total`
- `calls_total`

## PromQL used
- Request rate (RPS) per series:
  - `sum by (service_name, span_name, peer_service) ( rate(({__name__=~"<metricRegex>", span_kind="SPAN_KIND_SERVER"}[5m])) )`
- Error rate per series:
  - `sum by (service_name, span_name, peer_service) ( rate(({__name__=~"<metricRegex>", span_kind="SPAN_KIND_SERVER", status_code="STATUS_CODE_ERROR"}[5m])) ) /
     sum by (service_name, span_name, peer_service) ( rate(({__name__=~"<metricRegex>", span_kind="SPAN_KIND_SERVER"}[5m])) )`
- Step: 1 minute
- Window (lookback): configurable (default 30 minutes)

Note: `<metricRegex>` is resolved to match all supported metric names shown above.

## Anomaly detection
- Univariate, per series (one score per timestamp).
- Normalization: z-score normalize the series before training.
- Isolation Forest:
  - 100 trees
  - Subsample size psi = `min(64, N)`
  - Score per point in [0,1]; higher is more anomalous.
- Endpoints return the top-K points per series by score (K=3 for “all” endpoints).
- Event emission: For each top anomaly with score >= threshold, a line is logged:
  - `anomaly detected: service=<service_name> metric=<rps|error_rate>`

## HTTP API
- `GET /healthz`
  - Returns 200 "ok"
- `GET /anomalies/all`
  - Detects anomalies on RPS for all server spans grouped by labels.
  - Response:
    - `windowMinutes`: number
    - `series`: number of series analyzed
    - `results`: array of per-series objects
      - `labels`: `{ service_name, span_name, peer_service }`
      - `points`: number of points analyzed
      - `top`: array of top anomalies
        - `{ time: RFC3339, value: float, score: float }`
    - `metric`: "rps"
- `GET /anomalies/all_error`
  - Same as above but on error rate.
  - `metric`: "error_rate"

## Startup behavior
- On startup, the service discovers which services exist by calling Mimir’s `/api/v1/series` with matchers for spanmetrics over the configured window.
- Retries automatically while Mimir/metrics warm up.
- Logs discovered services once available:
  - `anomalies will be detected on services (N): svc-a, svc-b, ...`

## Configuration
Environment variables:
- `MIMIR_URL` (default: `http://mimir:9009/prometheus`)
- `IF_LISTEN_ADDR` (default: `:9030`)
- `WINDOW_MINUTES` (default: `30`)
- `ANOMALY_SCORE_THRESHOLD` (default: `0.6`)

## Run it (Docker Compose)
This repo includes a full demo stack: Mimir, OTel Collector, Grafana, the if-service, and sample services A–D.

- Build and start everything:
  - `docker compose up -d --build`
- Restart only if-service after code changes:
  - `docker compose build if-service`
  - `docker compose up -d if-service`

## Try it
- Health:
  - `curl http://localhost:9030/healthz`
- All-span RPS anomalies:
  - `curl http://localhost:9030/anomalies/all | jq` (optional jq for readability)
- All-span error-rate anomalies:
  - `curl http://localhost:9030/anomalies/all_error | jq`

Expected logs for anomalies above the threshold:
- `anomaly detected: service=service-d metric=rps`
- `anomaly detected: service=service-b metric=error_rate`

## Implementation highlights
- Language: Go 1.22
- Container: Distroless (nonroot), port 9030
- Key files:
  - `main.go` — HTTP server, PromQL queries, scoring, endpoints
  - `internal/iforest/iforest.go` — minimal 1D Isolation Forest
  - `internal/mimir/client.go` — Mimir/Prometheus HTTP API client (`/api/v1/query_range`, `/api/v1/series`)
- Value sanitation: NaN/Inf values from Prometheus are coerced to 0 to avoid instability during training.

## Limitations
- Univariate detection only (per-series RPS or error rate). No multivariate modeling yet.
- No authentication on endpoints; Mimir URL must be reachable from the container.
- Fixed step (1m) and rate window (5m) are not yet configurable.
- Scores are relative to the chosen window; changing window length changes anomaly sensitivity.
- Top-K per series is fixed at 3 for the "all" endpoints.

## Tuning
- `WINDOW_MINUTES`: increase for more context (more stable), decrease for faster detection of recent changes.
- `ANOMALY_SCORE_THRESHOLD`: raise to reduce noise, lower to be more sensitive.
- For deeper tuning, you can adjust constants in `detectAnomalies` (number of trees, subsample size) in code.

## Troubleshooting
- `anomaly detection startup: could not list services yet (err=...)`
  - Mimir may still be warming up or no spanmetrics are present yet.
  - The service retries discovery; wait 30–60 seconds.
  - Ensure the OTel Collector is running and exporting spanmetrics to Mimir.
- Empty results from endpoints:
  - Verify traffic exists between demo services (A→B→C→D) and that the collector is receiving traces.
  - Check Grafana for metrics presence.

## Roadmap ideas
- Add multivariate models (combine RPS, error rate, latency).
- Configurable query step and rate windows.
- Authentication and RBAC for endpoints.
- Push events to a webhook or message bus instead of stdout.
