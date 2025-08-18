package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	mimir "mcp/internal/mimir"
)

// Basic MCP JSON-RPC 2.0 messages
type req struct {
	ID      any             `json:"id"`
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type resp struct {
	ID      any       `json:"id"`
	JSONRPC string    `json:"jsonrpc"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type server struct{ c *mimir.Client }

func newServer() *server {
	base := getenv("MIMIR_URL", "http://mimir:9009/prometheus")
	return &server{c: mimir.New(base)}
}

func (s *server) handle(r req) resp {
	switch r.Method {
	case "initialize":
		// Minimal MCP handshake
		return ok(r.ID, map[string]any{
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "mimir-servicegraph", "version": "0.1.0"},
		})
	case "tools/list":
		// Advertise two tools with JSON Schemas
		return ok(r.ID, map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "servicegraph_topology",
					"description": "Return client->server edge weights from servicegraph_request_total over a recent window",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"windowMinutes": map[string]any{"type": "integer", "minimum": 1, "default": 10},
						},
					},
				},
				map[string]any{
					"name":        "servicegraph_latency_p95",
					"description": "Return p95 server-side latency for a given client->server edge using spanmetrics histogram",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"client", "server"},
						"properties": map[string]any{
							"client":        map[string]any{"type": "string"},
							"server":        map[string]any{"type": "string"},
							"windowMinutes": map[string]any{"type": "integer", "minimum": 1, "default": 10},
						},
					},
				},
				// New: generic spanmetrics latency quantile
				map[string]any{
					"name":        "spanmetrics_latency_quantile",
					"description": "Return latency quantile for a client->server edge using spanmetrics histogram",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"client", "server", "quantile"},
						"properties": map[string]any{
							"client":        map[string]any{"type": "string"},
							"server":        map[string]any{"type": "string"},
							"quantile":      map[string]any{"type": "number", "minimum": 0, "maximum": 1, "default": 0.95},
							"windowMinutes": map[string]any{"type": "integer", "minimum": 1, "default": 10},
						},
					},
				},
				// New: spanmetrics requests-per-second
				map[string]any{
					"name":        "spanmetrics_rps",
					"description": "Return requests per second for a client->server edge using spanmetrics count",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"server"},
						"properties": map[string]any{
							"server":        map[string]any{"type": "string"},
							"client":        map[string]any{"type": "string"},
							"windowMinutes": map[string]any{"type": "integer", "minimum": 1, "default": 10},
						},
					},
				},
				// New: top callers by RPS
				map[string]any{
					"name":        "spanmetrics_top_callers",
					"description": "Top-N callers (peer_service) to a server by request rate",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"server"},
						"properties": map[string]any{
							"server":        map[string]any{"type": "string"},
							"limit":         map[string]any{"type": "integer", "minimum": 1, "default": 5},
							"windowMinutes": map[string]any{"type": "integer", "minimum": 1, "default": 10},
						},
					},
				},
				// New: top endpoints by RPS
				map[string]any{
					"name":        "spanmetrics_top_endpoints",
					"description": "Top-N span names (endpoints) for a server by request rate",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"server"},
						"properties": map[string]any{
							"server":        map[string]any{"type": "string"},
							"limit":         map[string]any{"type": "integer", "minimum": 1, "default": 5},
							"windowMinutes": map[string]any{"type": "integer", "minimum": 1, "default": 10},
						},
					},
				},
			},
		})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil {
			return fail(r.ID, -32602, err)
		}
		switch p.Name {
		case "servicegraph_topology":
			var a struct {
				WindowMinutes int `json:"windowMinutes"`
			}
			_ = json.Unmarshal(p.Arguments, &a)
			if a.WindowMinutes <= 0 {
				a.WindowMinutes = 10
			}
			out, err := s.getTopology(a.WindowMinutes)
			if err != nil {
				return fail(r.ID, -32000, err)
			}
			return ok(r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}})
		case "servicegraph_latency_p95":
			var a struct {
				Client, Server string
				WindowMinutes  int
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return fail(r.ID, -32602, err)
			}
			if a.WindowMinutes <= 0 {
				a.WindowMinutes = 10
			}
			if a.Client == "" || a.Server == "" {
				return fail(r.ID, -32602, fmt.Errorf("client and server required"))
			}
			out, err := s.getLatency(a.Client, a.Server, a.WindowMinutes)
			if err != nil {
				return fail(r.ID, -32000, err)
			}
			return ok(r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}})
		case "spanmetrics_latency_quantile":
			var a struct {
				Client, Server string
				Quantile       float64
				WindowMinutes  int
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return fail(r.ID, -32602, err)
			}
			if a.WindowMinutes <= 0 {
				a.WindowMinutes = 10
			}
			if a.Quantile <= 0 || a.Quantile >= 1 {
				a.Quantile = 0.95
			}
			if a.Client == "" || a.Server == "" {
				return fail(r.ID, -32602, fmt.Errorf("client and server required"))
			}
			out, err := s.getLatencyQuantile(a.Client, a.Server, a.Quantile, a.WindowMinutes)
			if err != nil {
				return fail(r.ID, -32000, err)
			}
			return ok(r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}})
		case "spanmetrics_rps":
			var a struct {
				Server, Client string
				WindowMinutes  int
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return fail(r.ID, -32602, err)
			}
			if a.WindowMinutes <= 0 {
				a.WindowMinutes = 10
			}
			out, err := s.getRPS(a.Server, a.Client, a.WindowMinutes)
			if err != nil {
				return fail(r.ID, -32000, err)
			}
			return ok(r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}})
		case "spanmetrics_top_callers":
			var a struct {
				Server               string
				Limit, WindowMinutes int
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return fail(r.ID, -32602, err)
			}
			if a.WindowMinutes <= 0 {
				a.WindowMinutes = 10
			}
			if a.Limit <= 0 {
				a.Limit = 5
			}
			out, err := s.getTopCallers(a.Server, a.Limit, a.WindowMinutes)
			if err != nil {
				return fail(r.ID, -32000, err)
			}
			return ok(r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}})
		case "spanmetrics_top_endpoints":
			var a struct {
				Server               string
				Limit, WindowMinutes int
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return fail(r.ID, -32602, err)
			}
			if a.WindowMinutes <= 0 {
				a.WindowMinutes = 10
			}
			if a.Limit <= 0 {
				a.Limit = 5
			}
			out, err := s.getTopEndpoints(a.Server, a.Limit, a.WindowMinutes)
			if err != nil {
				return fail(r.ID, -32000, err)
			}
			return ok(r.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": string(out)}}})
		default:
			return fail(r.ID, -32601, fmt.Errorf("unknown tool: %s", p.Name))
		}
	case "shutdown":
		return ok(r.ID, map[string]any{})
	default:
		return fail(r.ID, -32601, fmt.Errorf("method not found"))
	}
}

// Query helpers
func (s *server) getTopology(windowM int) (json.RawMessage, error) {
	ctx := context.Background()
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := 30 * time.Second
	// Use the OTEL servicegraph connector metric name
	q := `sum by (client, server) (increase(traces_service_graph_request_total[5m]))`
	return s.c.QueryRange(ctx, q, start, end, step)
}

func (s *server) getLatency(client, serverName string, windowM int) (json.RawMessage, error) {
	ctx := context.Background()
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := 30 * time.Second
	// Use spanmetrics histogram exported by the collector's spanmetrics connector
	// Labels: service_name (server), peer_service (client), span_kind (SERVER)
	// Support multiple possible metric names via __name__ regex for robustness across versions.
	q := fmt.Sprintf(`histogram_quantile(0.95, sum by (le) (rate(({__name__=~"traces_span_metrics_duration_milliseconds_bucket|duration_milliseconds_bucket|rpc_server_duration_milliseconds_bucket", service_name="%s", peer_service="%s", span_kind="SPAN_KIND_SERVER"}[5m]))))`, serverName, client)
	return s.c.QueryRange(ctx, q, start, end, step)
}

// getLatencyQuantile returns a latency quantile for a client->server edge using spanmetrics histogram buckets.
func (s *server) getLatencyQuantile(client, serverName string, q float64, windowM int) (json.RawMessage, error) {
	ctx := context.Background()
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := 30 * time.Second
	prom := fmt.Sprintf(`histogram_quantile(%g, sum by (le) (rate(({__name__=~"traces_span_metrics_duration_milliseconds_bucket|duration_milliseconds_bucket|rpc_server_duration_milliseconds_bucket", service_name="%s", peer_service="%s", span_kind="SPAN_KIND_SERVER"}[5m]))))`, q, serverName, client)
	return s.c.QueryRange(ctx, prom, start, end, step)
}

// getRPS returns request rate for server (optionally by client) using spanmetrics count metric.
func (s *server) getRPS(serverName, client string, windowM int) (json.RawMessage, error) {
	ctx := context.Background()
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := 30 * time.Second
	// Use spanmetrics calls_total for request rate. Fallback to namespaced variant if present.
	filter := fmt.Sprintf(`service_name="%s", span_kind="SPAN_KIND_SERVER"`, serverName)
	if client != "" {
		filter += fmt.Sprintf(",peer_service=\"%s\"", client)
	}
	prom := fmt.Sprintf(`sum(rate(({__name__=~"traces_span_metrics_calls_total|calls_total", %s}[5m])))`, filter)
	return s.c.QueryRange(ctx, prom, start, end, step)
}

// getTopCallers returns top-N callers by request rate to a given server.
func (s *server) getTopCallers(serverName string, limit, windowM int) (json.RawMessage, error) {
	ctx := context.Background()
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := 30 * time.Second
	prom := fmt.Sprintf(`topk(%d, sum by (peer_service) (rate(({__name__=~"traces_span_metrics_calls_total|calls_total", service_name="%s", span_kind="SPAN_KIND_SERVER"}[5m]))))`, limit, serverName)
	return s.c.QueryRange(ctx, prom, start, end, step)
}

// getTopEndpoints returns top-N span names for a server by request rate.
func (s *server) getTopEndpoints(serverName string, limit, windowM int) (json.RawMessage, error) {
	ctx := context.Background()
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := 30 * time.Second
	prom := fmt.Sprintf(`topk(%d, sum by (span_name) (rate(({__name__=~"traces_span_metrics_calls_total|calls_total", service_name="%s", span_kind="SPAN_KIND_SERVER"}[5m]))))`, limit, serverName)
	return s.c.QueryRange(ctx, prom, start, end, step)
}

func main() {
	log.SetFlags(0)
	s := newServer()
	addr := getenv("MCP_LISTEN_ADDR", ":9020")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var in req
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.handle(in))
	})

	log.Printf("mcp http server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("http server error: %v", err)
	}
}

func ok(id any, res any) resp { return resp{ID: id, JSONRPC: "2.0", Result: res} }
func fail(id any, code int, err error) resp {
	return resp{ID: id, JSONRPC: "2.0", Error: &rpcError{Code: code, Message: err.Error()}}
}
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
