package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestResponsesAndTraces(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	measurements, err := newMetrics(noop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(provider, measurements, "http://127.0.0.1:3001")
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/ok", 200},
		{"GET", "/slow", 200},
		{"GET", "/error", 500},
		{"GET", "/missing", 404},
		{"POST", "/ok", 405},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			exporter.Reset()
			response := httptest.NewRecorder()
			start := time.Now()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			var body struct {
				TraceID string `json:"traceId"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			id, err := trace.TraceIDFromHex(body.TraceID)
			if err != nil || !id.IsValid() {
				t.Fatalf("invalid trace ID: %q", body.TraceID)
			}
			spans := exporter.GetSpans()
			wantCount := 1
			if tc.path == "/slow" {
				wantCount = 2
			}
			if len(spans) != wantCount {
				t.Fatalf("span count = %d, want %d", len(spans), wantCount)
			}
			parent := spans[len(spans)-1]
			if parent.SpanContext.TraceID() != id || parent.SpanKind != trace.SpanKindServer {
				t.Fatal("response trace ID must match the HTTP server span")
			}
			if tc.path == "/slow" {
				child := spans[0]
				if time.Since(start) < time.Second || child.Name != "simulate-work" ||
					child.SpanContext.TraceID() != id || child.Parent.SpanID() != parent.SpanContext.SpanID() {
					t.Fatal("slow request must wait one second and record a child span")
				}
			}
			if tc.path == "/error" {
				if parent.Status.Code != codes.Error || len(parent.Events) != 1 || parent.Events[0].Name != "exception" {
					t.Fatal("error request must record an exception and ERROR status")
				}
			}
		})
	}
}
