// Package work implements API B's small, independently served workload.
package work

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Result struct {
	Service      string `json:"service"`
	Mode         string `json:"mode"`
	TraceID      string `json:"traceId"`
	SpanID       string `json:"spanId"`
	ParentSpanID string `json:"parentSpanId"`
	WorkSpanID   string `json:"workSpanId"`
	Traceparent  string `json:"traceparent"`
	Error        string `json:"error,omitempty"`
}

func NewHandler(tp trace.TracerProvider, mp metric.MeterProvider) http.Handler {
	tracer := tp.Tracer("playground")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/work" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", 405)
			return
		}
		mode := r.URL.Query().Get("mode")
		if mode == "" {
			mode = "ok"
		}
		if mode != "ok" && mode != "slow" && mode != "error" {
			http.Error(w, "mode must be ok, slow, or error", 400)
			return
		}
		serverSpan := trace.SpanFromContext(r.Context())
		// 表示のため受信ヘッダーを読む。実際の親子関係はotelhttpが構築する。
		remote := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(), propagation.HeaderCarrier(r.Header)))
		_, child := tracer.Start(r.Context(), "simulate-work")
		child.SetAttributes(attribute.String("demo.mode", mode))
		result := Result{Service: "api-b", Mode: mode, TraceID: serverSpan.SpanContext().TraceID().String(), SpanID: serverSpan.SpanContext().SpanID().String(), WorkSpanID: child.SpanContext().SpanID().String(), Traceparent: r.Header.Get("traceparent")}
		if remote.IsValid() {
			result.ParentSpanID = remote.SpanID().String()
		}
		status := http.StatusOK
		if mode == "slow" {
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				child.RecordError(r.Context().Err())
				child.SetStatus(codes.Error, "request canceled")
				child.End()
				return
			}
		}
		if mode == "error" {
			err := errors.New("Intentional API B error")
			child.RecordError(err)
			child.SetStatus(codes.Error, err.Error())
			serverSpan.RecordError(err)
			serverSpan.SetStatus(codes.Error, err.Error())
			result.Error = err.Error()
			status = http.StatusInternalServerError
		}
		child.End()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	})
	return otelhttp.NewHandler(handler, "GET /work", otelhttp.WithTracerProvider(tp), otelhttp.WithMeterProvider(mp), otelhttp.WithPropagators(propagation.TraceContext{}))
}
