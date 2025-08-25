package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"ifservice/internal/iforest"
	mimir "ifservice/internal/mimir"
)

type promMatrix struct {
	ResultType string       `json:"resultType"`
	Result     []promSeries `json:"result"`
}

type promSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][]interface{}   `json:"values"`
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func sane(v float64) (float64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// PromQL regex to match spanmetrics call counters across versions
const metricRegex = `traces_spanmetrics_calls_total|traces_span_metrics_calls_total|calls_total`

// logAnomalyEvents writes one log line per anomaly above the threshold.
// For now, the event includes only service_name and metric type as requested.
func logAnomalyEvents(serviceName, metric string, topIdx []int, scores []float64, threshold float64) {
	for _, i := range topIdx {
		if i >= 0 && i < len(scores) && scores[i] >= threshold {
			log.Printf("anomaly detected: service=%s metric=%s", serviceName, metric)
		}
	}
}

// fetchAllRPS pulls spanmetrics RPS for ALL server spans, grouped by service/span/peer, over a window
func fetchAllRPS(ctx context.Context, c *mimir.Client, windowM int) ([]promSeries, [][]float64, [][]time.Time, error) {
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := time.Minute
	// Group by key labels to keep one series per span endpoint and caller
	// Supports both upstream metric names used by spanmetrics connector
	q := `sum by (service_name, span_name, peer_service) (rate(({__name__=~"` + metricRegex + `", span_kind="SPAN_KIND_SERVER"}[5m])))`
	raw, err := c.QueryRange(ctx, q, start, end, step)
	if err != nil {
		return nil, nil, nil, err
	}
	var resp struct {
		Data promMatrix `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, nil, err
	}
	if len(resp.Data.Result) == 0 {
		return nil, nil, nil, fmt.Errorf("no data")
	}
	series := resp.Data.Result
	allVals := make([][]float64, len(series))
	allTs := make([][]time.Time, len(series))
	for i, s := range series {
		vals := make([]float64, 0, len(s.Values))
		ts := make([]time.Time, 0, len(s.Values))
		for _, v := range s.Values {
			if len(v) != 2 {
				continue
			}
			sec, _ := v[0].(float64)
			str, _ := v[1].(string)
			var f float64
			fmt.Sscan(str, &f)
			if fv, ok := sane(f); ok {
				vals = append(vals, fv)
			} else {
				vals = append(vals, 0)
			}
			ts = append(ts, time.Unix(int64(sec), 0))
		}
		allVals[i] = vals
		allTs[i] = ts
	}
	return series, allVals, allTs, nil
}

// fetchAllErrorRate pulls error rate (error calls / total calls) for ALL server spans
// grouped by service/span/peer over a window
func fetchAllErrorRate(ctx context.Context, c *mimir.Client, windowM int) ([]promSeries, [][]float64, [][]time.Time, error) {
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := time.Minute
	q := `sum by (service_name, span_name, peer_service) (rate(({__name__=~"` + metricRegex + `", span_kind="SPAN_KIND_SERVER", status_code="STATUS_CODE_ERROR"}[5m]))) /
		  sum by (service_name, span_name, peer_service) (rate(({__name__=~"` + metricRegex + `", span_kind="SPAN_KIND_SERVER"}[5m])))`
	raw, err := c.QueryRange(ctx, q, start, end, step)
	if err != nil {
		return nil, nil, nil, err
	}
	var resp struct {
		Data promMatrix `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, nil, err
	}
	if len(resp.Data.Result) == 0 {
		return nil, nil, nil, fmt.Errorf("no data")
	}
	series := resp.Data.Result
	allVals := make([][]float64, len(series))
	allTs := make([][]time.Time, len(series))
	for i, s := range series {
		vals := make([]float64, 0, len(s.Values))
		ts := make([]time.Time, 0, len(s.Values))
		for _, v := range s.Values {
			if len(v) != 2 {
				continue
			}
			sec, _ := v[0].(float64)
			str, _ := v[1].(string)
			var f float64
			fmt.Sscan(str, &f)
			if fv, ok := sane(f); ok {
				vals = append(vals, fv)
			} else {
				vals = append(vals, 0)
			}
			ts = append(ts, time.Unix(int64(sec), 0))
		}
		allVals[i] = vals
		allTs[i] = ts
	}
	return series, allVals, allTs, nil
}

// fetchServices returns distinct service_name values that have server-side spans in the window
func fetchServices(ctx context.Context, c *mimir.Client, windowM int) ([]string, error) {
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	// Ask for any spanmetrics calls series within window and parse labels
	matchers := []string{`{__name__=~"` + metricRegex + `"}`}
	raw, err := c.Series(ctx, matchers, start, end)
	if err != nil {
		return nil, err
	}
	var data []map[string]string
	var payload struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}
	// The client returns qr.Data which is already the raw Data field, so unmarshal directly
	if err := json.Unmarshal(raw, &data); err == nil {
		payload.Data = data
	} else if err := json.Unmarshal(raw, &payload); err == nil {
		// some proxies may wrap differently
	}
	seen := map[string]struct{}{}
	for _, lbls := range payload.Data {
		if name := lbls["service_name"]; name != "" {
			seen[name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// fetchRPS pulls spanmetrics RPS for a server (optionally by client) over a window
// Deprecated: single-target fetch, no longer used
func fetchRPS(ctx context.Context, c *mimir.Client, server, client string, windowM int) ([]float64, []time.Time, error) {
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := time.Minute
	filter := fmt.Sprintf(`service_name="%s", span_kind="SPAN_KIND_SERVER"`, server)
	if client != "" {
		filter += fmt.Sprintf(",peer_service=\"%s\"", client)
	}
	q := fmt.Sprintf(`sum(rate(({__name__=~"%s", %s}[5m])))`, metricRegex, filter)
	raw, err := c.QueryRange(ctx, q, start, end, step)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Data promMatrix `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, err
	}
	if len(resp.Data.Result) == 0 {
		return nil, nil, fmt.Errorf("no data")
	}
	series := resp.Data.Result[0]
	vals := make([]float64, 0, len(series.Values))
	ts := make([]time.Time, 0, len(series.Values))
	for _, v := range series.Values {
		if len(v) != 2 {
			continue
		}
		sec, _ := v[0].(float64)
		s := v[1].(string)
		var f float64
		fmt.Sscan(s, &f)
		if fv, ok := sane(f); ok {
			vals = append(vals, fv)
		} else {
			vals = append(vals, 0)
		}
		ts = append(ts, time.Unix(int64(sec), 0))
	}
	return vals, ts, nil
}

// fetchErrorRate pulls error rate for a specific server (optionally by client)
// Deprecated: single-target fetch, no longer used
func fetchErrorRate(ctx context.Context, c *mimir.Client, server, client string, windowM int) ([]float64, []time.Time, error) {
	end := time.Now()
	start := end.Add(-time.Duration(windowM) * time.Minute)
	step := time.Minute
	filter := fmt.Sprintf(`service_name="%s", span_kind="SPAN_KIND_SERVER"`, server)
	if client != "" {
		filter += fmt.Sprintf(",peer_service=\"%s\"", client)
	}
	q := fmt.Sprintf(`sum(rate(({__name__=~"%s", %s, status_code="STATUS_CODE_ERROR"}[5m])))/sum(rate(({__name__=~"%s", %s}[5m])))`, metricRegex, filter, metricRegex, filter)
	raw, err := c.QueryRange(ctx, q, start, end, step)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Data promMatrix `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, nil, err
	}
	if len(resp.Data.Result) == 0 {
		return nil, nil, fmt.Errorf("no data")
	}
	series := resp.Data.Result[0]
	vals := make([]float64, 0, len(series.Values))
	ts := make([]time.Time, 0, len(series.Values))
	for _, v := range series.Values {
		if len(v) != 2 {
			continue
		}
		sec, _ := v[0].(float64)
		s := v[1].(string)
		var f float64
		fmt.Sscan(s, &f)
		if fv, ok := sane(f); ok {
			vals = append(vals, fv)
		} else {
			vals = append(vals, 0)
		}
		ts = append(ts, time.Unix(int64(sec), 0))
	}
	return vals, ts, nil
}

// detectAnomalies trains an IF on the window and returns the top-k anomalous points.
func detectAnomalies(vals []float64, k int) ([]int, []float64) {
	// Normalize (z-score) to stabilize splits
	mu, sd := meanStd(vals)
	norm := make([]float64, len(vals))
	for i, v := range vals {
		norm[i] = (v - mu) / (sd + 1e-9)
	}
	f := iforest.New(norm, 100, min(64, len(norm)))
	scores := make([]float64, len(norm))
	for i, v := range norm {
		scores[i] = f.Score(v)
	}
	idx := make([]int, len(norm))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return scores[idx[i]] > scores[idx[j]] })
	if k > len(idx) {
		k = len(idx)
	}
	return idx[:k], scores
}

func meanStd(x []float64) (float64, float64) {
	if len(x) == 0 {
		return 0, 1
	}
	sum := 0.0
	for _, v := range x {
		sum += v
	}
	mu := sum / float64(len(x))
	ss := 0.0
	for _, v := range x {
		d := v - mu
		ss += d * d
	}
	return mu, math.Sqrt(ss / float64(len(x)))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	log.SetFlags(0)
	mimirURL := getenv("MIMIR_URL", "http://mimir:9009/prometheus")

	window := 30
	if v := getenv("WINDOW_MINUTES", ""); v != "" {
		fmt.Sscanf(v, "%d", &window)
	}
	// anomaly score threshold for logging events (0..1). Default 0.6
	threshold := 0.6
	if v := getenv("ANOMALY_SCORE_THRESHOLD", ""); v != "" {
		fmt.Sscanf(v, "%f", &threshold)
	}

	c := mimir.New(mimirURL)

	// Discover and log which services we will detect anomalies on (for /anomalies/all* endpoints)
	func() {
		var services []string
		var err error
		for attempt := 1; attempt <= 30; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			services, err = fetchServices(ctx, c, window)
			cancel()
			if err == nil && len(services) > 0 {
				break
			}
			if attempt == 1 {
				log.Printf("anomaly detection startup: waiting for metrics (attempt %d): %v", attempt, err)
			}
			time.Sleep(2 * time.Second)
		}
		if err != nil || len(services) == 0 {
			log.Printf("anomaly detection startup: could not list services yet (err=%v)", err)
			return
		}
		log.Printf("anomalies will be detected on services (%d): %s", len(services), strings.Join(services, ", "))
	}()

	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200); w.Write([]byte("ok")) })

	// New: anomalies for ALL spans grouped by service_name/span_name/peer_service
	http.HandleFunc("/anomalies/all", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		series, allVals, allTs, err := fetchAllRPS(ctx, c, window)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		// Build per-series results
		results := make([]map[string]any, 0, len(series))
		for i, s := range series {
			vals := allVals[i]
			ts := allTs[i]
			if len(vals) == 0 {
				continue
			}
			// top-3 per series
			idx, scores := detectAnomalies(vals, 3)
			// fire events for RPS anomalies per service
			svc := s.Metric["service_name"]
			if svc == "" {
				svc = "unknown"
			}
			logAnomalyEvents(svc, "rps", idx, scores, threshold)
			top := make([]map[string]any, 0, len(idx))
			for _, j := range idx {
				top = append(top, map[string]any{
					"time":  ts[j].Format(time.RFC3339),
					"value": vals[j],
					"score": scores[j],
				})
			}
			// pick only the key identifying labels to keep payload tidy
			labels := map[string]string{
				"service_name": s.Metric["service_name"],
				"span_name":    s.Metric["span_name"],
				"peer_service": s.Metric["peer_service"],
			}
			results = append(results, map[string]any{
				"labels": labels,
				"points": len(vals),
				"top":    top,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"windowMinutes": window,
			"series":        len(results),
			"results":       results,
			"metric":        "rps",
		})
	})

	// anomalies for ALL spans using error rate
	http.HandleFunc("/anomalies/all_error", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		series, allVals, allTs, err := fetchAllErrorRate(ctx, c, window)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		results := make([]map[string]any, 0, len(series))
		for i, s := range series {
			vals := allVals[i]
			ts := allTs[i]
			if len(vals) == 0 {
				continue
			}
			idx, scores := detectAnomalies(vals, 3)
			// fire events for error rate anomalies per service
			svc := s.Metric["service_name"]
			if svc == "" {
				svc = "unknown"
			}
			logAnomalyEvents(svc, "error_rate", idx, scores, threshold)
			top := make([]map[string]any, 0, len(idx))
			for _, j := range idx {
				top = append(top, map[string]any{
					"time":  ts[j].Format(time.RFC3339),
					"value": vals[j],
					"score": scores[j],
				})
			}
			labels := map[string]string{
				"service_name": s.Metric["service_name"],
				"span_name":    s.Metric["span_name"],
				"peer_service": s.Metric["peer_service"],
			}
			results = append(results, map[string]any{
				"labels": labels,
				"points": len(vals),
				"top":    top,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"windowMinutes": window,
			"series":        len(results),
			"results":       results,
			"metric":        "error_rate",
		})
	})

	addr := getenv("IF_LISTEN_ADDR", ":9030")
	log.Printf("isolation-forest service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
