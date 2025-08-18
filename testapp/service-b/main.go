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
	shutdown, err := otelx.Init(ctx, "service-b", endpoint)
	if err != nil {
		log.Fatalf("failed to init otel: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		tr := otel.Tracer("service-b")
		ctx, span := tr.Start(r.Context(), "service-b handle /hello")
		defer span.End()

		// Call service-c for further processing
		client := otelx.NewHTTPClient("service-b")
		cURL := getenv("SERVICE_C_URL", "http://service-c:8082/work")
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("error calling service-c: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		fmt.Fprintln(w, "hello from service-b -> service-c")
	})

	handler := otelhttp.NewHandler(otelx.WithPeerServiceAttribute(mux), "service-b-server")
	log.Println("service-b listening on :8081")
	// Periodic self-hit to generate server spans ~5/min
	go func() {
		rate := getDurationEnv("SERVICE_B_RATE", 12*time.Second)
		ticker := time.NewTicker(rate)
		defer ticker.Stop()
		client := otelx.NewHTTPClient("service-b")
		for range ticker.C {
			_, _ = client.Get("http://localhost:8081/hello")
		}
	}()
	log.Fatal(http.ListenAndServe(":8081", handler))
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
