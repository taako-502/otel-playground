package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/taako-502/otel-playground/internal/telemetry"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"

	"go.opentelemetry.io/otel/trace"
)

//go:embed index.html
var indexHTML []byte

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	providers, shutdown, err := telemetry.New("api-a")
	if err != nil {
		return err
	}
	defer shutdown()
	provider, meterProvider := providers.Traces, providers.Metrics

	measurements, err := newMetrics(meterProvider)
	if err != nil {
		return err
	}

	downstreamURL := os.Getenv("API_B_URL")
	if downstreamURL == "" {
		downstreamURL = "http://127.0.0.1:3001"
	}
	parsed, err := url.Parse(downstreamURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("API_B_URL must be an http(s) origin, e.g. http://127.0.0.1:3001")
	}
	return telemetry.Serve(newHandler(provider, measurements, strings.TrimRight(downstreamURL, "/")), "3000")
}

func newHandler(provider trace.TracerProvider, measurements *metrics, downstreamURL string) http.Handler {
	tracer := provider.Tracer("playground")
	propagated := downstreamClient(provider, measurements.provider, propagation.TraceContext{})
	isolated := downstreamClient(provider, measurements.provider, propagation.NewCompositeTextMapPropagator())
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			measurements.receive(w, r)
			return
		}
		isAPI := r.Method == http.MethodGet && (r.URL.Path == "/ok" || r.URL.Path == "/slow" || r.URL.Path == "/error" || r.URL.Path == "/downstream")
		if isAPI {
			measurements.requests.Add(r.Context(), 1, metric.WithAttributes(attribute.String("route", r.URL.Path)))
		}
		span := trace.SpanFromContext(r.Context())
		reply := func(status int, body map[string]any) {
			if isAPI && status >= 500 {
				measurements.errors.Add(r.Context(), 1, metric.WithAttributes(attribute.String("route", r.URL.Path)))
			}
			body["traceId"] = span.SpanContext().TraceID().String()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
		}
		if r.Method != http.MethodGet {
			reply(http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
			return
		}
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(indexHTML)
		case "/downstream":
			callDownstream(r, downstreamURL, propagated, isolated, reply)
		case "/ok":
			reply(http.StatusOK, map[string]any{"message": "ok"})
		case "/slow":
			// リクエストのContextを渡し、HTTPスパンの子として記録する。
			_, child := tracer.Start(r.Context(), "simulate-work")
			child.SetAttributes(attribute.Int("demo.delay_ms", 1000))
			time.Sleep(time.Second)
			child.End()
			reply(http.StatusOK, map[string]any{"message": "slow", "delayMs": 1000})
		case "/error":
			err := errors.New("Intentional demo error")
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			reply(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		default:
			reply(http.StatusNotFound, map[string]any{"error": "Use /ok, /slow, or /error"})
		}
	})
	return otelhttp.NewHandler(handler, "HTTP request",
		otelhttp.WithTracerProvider(provider),
		otelhttp.WithMeterProvider(measurements.provider),
		otelhttp.WithPropagators(propagation.TraceContext{}),
	)
}
