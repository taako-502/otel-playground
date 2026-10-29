package main

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/taako-502/otel-playground/internal/work"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func downstreamClient(tp trace.TracerProvider, mp metric.MeterProvider, propagator propagation.TextMapPropagator) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport,
		otelhttp.WithTracerProvider(tp), otelhttp.WithMeterProvider(mp), otelhttp.WithPropagators(propagator)),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func callDownstream(r *http.Request, origin string, propagated, isolated *http.Client, reply func(int, map[string]any)) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "ok"
	}
	propagationMode := r.URL.Query().Get("propagate")
	if propagationMode == "" {
		propagationMode = "true"
	}
	if (mode != "ok" && mode != "slow" && mode != "error") || (propagationMode != "true" && propagationMode != "false") {
		reply(400, map[string]any{"error": "mode must be ok/slow/error; propagate must be true/false"})
		return
	}
	delayA := r.URL.Query().Get("delay_a")
	if delayA != "" && delayA != "true" && delayA != "false" {
		reply(400, map[string]any{"error": "delay_a must be true/false"})
		return
	}
	if delayA == "true" {
		// Bを呼ぶ前にAで待機する。Bの遅延とウォーターフォールで比較する。
		_, child := trace.SpanFromContext(r.Context()).TracerProvider().Tracer("playground").Start(r.Context(), "prepare-work")
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
		child.End()
	}
	client := propagated
	if propagationMode == "false" {
		client = isolated
	}
	span := trace.SpanFromContext(r.Context())
	// Contextを引き継ぐことでAのSERVERスパンの子にCLIENTスパンを作る。
	// 伝播なしでもこの親子関係とキャンセルは保ち、ヘッダーへの注入だけを止める。
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, origin+"/work?mode="+mode, nil)
	if err != nil {
		reply(502, map[string]any{"error": "Invalid API B URL"})
		return
	}
	response, err := client.Do(request)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "API B request failed")
		reply(502, map[string]any{"error": "API Bに接続できません。起動状態とAPI_B_URLを確認してください。"})
		return
	}
	defer response.Body.Close()
	var result work.Result
	if err := json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&result); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Invalid API B response")
		reply(502, map[string]any{"error": "Invalid API B response"})
		return
	}
	// ボディを最後まで読み、CLIENTスパンを応答処理中に終了させる。
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 16384))
	_ = response.Body.Close()
	status := http.StatusOK
	if response.StatusCode >= 400 {
		status = 502
		span.SetStatus(codes.Error, "API B returned an error")
	}
	reply(status, map[string]any{
		"service": "api-a", "spanId": span.SpanContext().SpanID().String(),
		"propagation": propagationMode == "true", "downstreamStatus": response.StatusCode,
		"downstream": result, "delayA": delayA == "true",
	})
}
