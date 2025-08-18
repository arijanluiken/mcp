package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	otelx "testapp/internal/otel"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
)

func main() {
	ctx := context.Background()
	endpoint := getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector:4317")
	shutdown, err := otelx.Init(ctx, "service-a", endpoint)
	if err != nil {
		log.Fatalf("failed to init otel: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		tr := otel.Tracer("service-a")
		ctx, span := tr.Start(r.Context(), "handle /call")
		defer span.End()

		client := otelx.NewHTTPClient("service-a")
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, getenv("SERVICE_B_URL", "http://service-b:8081/hello"), nil)
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("error calling service-b: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		_, _ = w.Write([]byte("service-a -> service-b OK"))
	})

	// Periodic traffic generator to produce ~5 tx/min (default every 12s)
	go func() {
		rate := getDurationEnv("SERVICE_A_RATE", 12*time.Second)
		ticker := time.NewTicker(rate)
		defer ticker.Stop()
		client := otelx.NewHTTPClient("service-a")
		for range ticker.C {
			_, _ = client.Get("http://localhost:8080/call")
		}
	}()

	handler := otelhttp.NewHandler(otelx.WithPeerServiceAttribute(mux), "service-a-server")
	log.Println("service-a listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", handler))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getDurationEnv(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
