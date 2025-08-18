package main

import (
	"context"
	"log"
	"math/rand"
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
	shutdown, err := otelx.Init(ctx, "service-d", endpoint)
	if err != nil {
		log.Fatalf("failed to init otel: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/do", func(w http.ResponseWriter, r *http.Request) {
		tr := otel.Tracer("service-d")
		_, span := tr.Start(r.Context(), "service-d do work")
		defer span.End()
		// Simulate variable work
		time.Sleep(time.Duration(50+rand.Intn(100)) * time.Millisecond)
		w.Write([]byte("done by service-d"))
	})

	handler := otelhttp.NewHandler(otelx.WithPeerServiceAttribute(mux), "service-d-server")
	log.Println("service-d listening on :8083")
	log.Fatal(http.ListenAndServe(":8083", handler))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
