package otelx

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	apitrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Init configures a global TracerProvider exporting to the OTEL collector at endpoint.
// endpoint example: "localhost:4317" or "otel-collector:4317"
func Init(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithDialOption(grpc.WithBlock()),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(500*time.Millisecond),
			sdktrace.WithMaxExportBatchSize(1024),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	// Ensure W3C TraceContext and Baggage are used for propagation across HTTP boundaries.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// NewHTTPClient returns an http.Client instrumented with otelhttp transport and
// automatically injects X-Peer-Service header with the caller's service name.
func NewHTTPClient(callerService string) http.Client {
	// Inner transport runs AFTER otelhttp starts the client span, so we can annotate it.
	inner := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		// clone request to avoid mutating shared headers in rare cases
		r := req.Clone(req.Context())
		r.Header = r.Header.Clone()
		// Tell the server who we are so it can set peer.service on its span (caller side name).
		r.Header.Set("X-Peer-Service", callerService)
		// Derive the target service from URL host (strip :port) and set it on the CLIENT span.
		target := r.URL.Host
		if i := strings.Index(target, ":"); i > 0 {
			target = target[:i]
		}
		if span := apitrace.SpanFromContext(r.Context()); span != nil {
			span.SetAttributes(attribute.String("peer.service", target))
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	// Instrument with otelhttp, which will create the client span and inject trace headers.
	return http.Client{Transport: otelhttp.NewTransport(inner)}
}

// WithPeerServiceAttribute wraps an http.Handler and, if X-Peer-Service header is present,
// sets the peer.service attribute on the active server span.
func WithPeerServiceAttribute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Peer-Service"); v != "" {
			// normalize to bare service name (strip :port if someone sent host)
			ps := v
			if i := strings.Index(ps, ":"); i > 0 {
				ps = ps[:i]
			}
			if span := apitrace.SpanFromContext(r.Context()); span != nil {
				span.SetAttributes(attribute.String("peer.service", ps))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// roundTripperFunc allows using a function as an http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
