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
	shutdown, err := otelx.Init(ctx, "service-c", endpoint)
	if err != nil {
		log.Fatalf("failed to init otel: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		tr := otel.Tracer("service-c")
		ctx, span := tr.Start(r.Context(), "service-c work")
		defer span.End()

		// Call service-d to perform deeper nested work
		client := otelx.NewHTTPClient("service-c")
		dURL := getenv("SERVICE_D_URL", "http://service-d:8083/do")
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, dURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("error calling service-d: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		_, _ = w.Write([]byte("service-c -> service-d OK"))
	})

	// Optional periodic traffic to generate standalone traces
	go func() {
		rate := getDurationEnv("SERVICE_C_RATE", 25*time.Second)
		ticker := time.NewTicker(rate)
		defer ticker.Stop()
		client := otelx.NewHTTPClient("service-c")
		for range ticker.C {
			_, _ = client.Get("http://localhost:8082/work")
		}
	}()

	handler := otelhttp.NewHandler(otelx.WithPeerServiceAttribute(mux), "service-c-server")
	log.Println("service-c listening on :8082")
	log.Fatal(http.ListenAndServe(":8082", handler))
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
