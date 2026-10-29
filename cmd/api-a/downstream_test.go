package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/taako-502/otel-playground/internal/work"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestDistributedTrace(t *testing.T) {
	for _, propagate := range []string{"true", "false"} {
		for _, mode := range []string{"ok", "slow", "error"} {
			t.Run(propagate+"/"+mode, func(t *testing.T) {
				exporter := tracetest.NewInMemoryExporter()
				makeProvider := func(service string) *sdktrace.TracerProvider {
					provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", service))))
					t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
					return provider
				}
				a, b := makeProvider("api-a"), makeProvider("api-b")
				mp := noop.NewMeterProvider()
				measurements, err := newMetrics(mp)
				if err != nil {
					t.Fatal(err)
				}
				serverB := httptest.NewServer(work.NewHandler(b, mp))
				defer serverB.Close()
				serverA := httptest.NewServer(newHandler(a, measurements, serverB.URL))
				defer serverA.Close()
				start := time.Now()
				response, err := http.Get(serverA.URL + "/downstream?mode=" + mode + "&propagate=" + propagate)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				var body struct {
					TraceID          string      `json:"traceId"`
					SpanID           string      `json:"spanId"`
					DownstreamStatus int         `json:"downstreamStatus"`
					Downstream       work.Result `json:"downstream"`
				}
				if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				wantA, wantB := 200, 200
				if mode == "error" {
					wantA, wantB = 502, 500
				}
				if response.StatusCode != wantA || body.DownstreamStatus != wantB {
					t.Fatalf("status A=%d B=%d", response.StatusCode, body.DownstreamStatus)
				}
				if mode == "slow" && time.Since(start) < time.Second {
					t.Fatal("slow request did not wait")
				}
				// HTTPレスポンス受信とAのSERVERスパン終了は並行するため、終了を待つ。
				deadline := time.Now().Add(time.Second)
				for len(exporter.GetSpans()) < 4 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				spans := exporter.GetSpans()
				if len(spans) != 4 {
					t.Fatalf("spans=%d, want 4", len(spans))
				}
				var as, ac, bs, child tracetest.SpanStub
				for _, s := range spans {
					service, _ := s.Resource.Set().Value("service.name")
					switch {
					case service.AsString() == "api-a" && s.SpanKind == trace.SpanKindServer:
						as = s
					case service.AsString() == "api-a" && s.SpanKind == trace.SpanKindClient:
						ac = s
					case service.AsString() == "api-b" && s.SpanKind == trace.SpanKindServer:
						bs = s
					case service.AsString() == "api-b" && s.Name == "simulate-work":
						child = s
					}
				}
				for _, s := range []tracetest.SpanStub{as, ac, bs, child} {
					if !s.SpanContext.IsValid() {
						t.Fatal("expected span missing")
					}
				}
				if ac.Parent.SpanID() != as.SpanContext.SpanID() || child.Parent.SpanID() != bs.SpanContext.SpanID() {
					t.Fatal("local parent-child links broken")
				}
				if body.TraceID != as.SpanContext.TraceID().String() || body.SpanID != as.SpanContext.SpanID().String() || body.Downstream.SpanID != bs.SpanContext.SpanID().String() || body.Downstream.WorkSpanID != child.SpanContext.SpanID().String() {
					t.Fatal("response does not describe actual spans")
				}
				if ac.SpanContext.TraceID() != as.SpanContext.TraceID() || child.SpanContext.TraceID() != bs.SpanContext.TraceID() {
					t.Fatal("local trace IDs differ")
				}
				if propagate == "true" {
					if bs.SpanContext.TraceID() != as.SpanContext.TraceID() || bs.Parent.SpanID() != ac.SpanContext.SpanID() || !bs.Parent.IsRemote() {
						t.Fatal("remote parent-child link broken")
					}
					if body.Downstream.ParentSpanID != ac.SpanContext.SpanID().String() || body.Downstream.Traceparent == "" {
						t.Fatal("received header/parent not displayed")
					}
				} else {
					if bs.SpanContext.TraceID() == as.SpanContext.TraceID() || bs.Parent.IsValid() || body.Downstream.Traceparent != "" || body.Downstream.ParentSpanID != "" {
						t.Fatal("disabled propagation still linked traces")
					}
				}
				if mode == "error" {
					for _, s := range spans {
						if s.Status.Code != codes.Error {
							t.Errorf("%s must have Error status", s.Name)
						}
					}
				}
			})
		}
	}
}

func TestDownstreamFailureAndValidation(t *testing.T) {
	mp := noop.NewMeterProvider()
	measurements, err := newMetrics(mp)
	if err != nil {
		t.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider()
	defer tp.Shutdown(context.Background())
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not JSON")) }))
	handler := newHandler(tp, measurements, b.URL)
	request := func(query string, want int) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/downstream"+query, nil))
		if response.Code != want {
			t.Fatalf("%s status=%d want=%d", query, response.Code, want)
		}
	}
	request("?mode=invalid", 400)
	request("?propagate=invalid", 400)
	request("", 502)
	b.Close()
	request("", 502)
}

func TestDelayInA(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())
	mp := noop.NewMeterProvider()
	measurements, err := newMetrics(mp)
	if err != nil {
		t.Fatal(err)
	}
	b := httptest.NewServer(work.NewHandler(tp, mp))
	defer b.Close()
	handler := newHandler(tp, measurements, b.URL)
	response := httptest.NewRecorder()
	start := time.Now()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/downstream?mode=ok&delay_a=true", nil))
	if response.Code != 200 || time.Since(start) < time.Second {
		t.Fatal("A delay did not complete")
	}
	spans := exporter.GetSpans()
	if len(spans) != 5 {
		t.Fatalf("spans=%d, want 5", len(spans))
	}
	var prepare, client, root tracetest.SpanStub
	for _, s := range spans {
		if s.Name == "prepare-work" {
			prepare = s
		}
		if s.SpanKind == trace.SpanKindClient {
			client = s
		}
		if s.SpanKind == trace.SpanKindServer && !s.Parent.IsValid() {
			root = s
		}
	}
	if !prepare.SpanContext.IsValid() || prepare.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatal("prepare-work must be a child of A server")
	}
	if prepare.EndTime.Sub(prepare.StartTime) < time.Second || prepare.EndTime.After(client.StartTime) {
		t.Fatal("A must wait before calling B")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/downstream?delay_a=invalid", nil))
	if response.Code != 400 {
		t.Fatal("invalid delay must be rejected")
	}
}
