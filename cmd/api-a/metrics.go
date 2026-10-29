package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type metrics struct {
	provider                           metric.MeterProvider
	requests, errors, clicks, jsErrors metric.Int64Counter
	navigation, fetch                  metric.Float64Histogram
}

func newMetrics(provider metric.MeterProvider) (*metrics, error) {
	m := &metrics{provider: provider}
	meter := provider.Meter("playground")
	var err error
	for _, item := range []struct {
		name   string
		target *metric.Int64Counter
	}{
		{"demo.api.requests", &m.requests}, {"demo.api.errors", &m.errors},
		{"demo.browser.clicks", &m.clicks}, {"demo.browser.errors", &m.jsErrors},
	} {
		*item.target, err = meter.Int64Counter(item.name)
		if err != nil {
			return nil, err
		}
	}
	for _, item := range []struct {
		name   string
		target *metric.Float64Histogram
	}{
		{"demo.browser.navigation.duration", &m.navigation}, {"demo.browser.fetch.duration", &m.fetch},
	} {
		*item.target, err = meter.Float64Histogram(item.name, metric.WithUnit("ms"),
			metric.WithExplicitBucketBoundaries(0, 10, 50, 100, 250, 500, 1000, 2000, 5000))
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// 学習用のイベント形式。ブラウザの計測値を受け取り、GoのOTel SDKで集計する。
// URLや例外メッセージは送らず、属性値の種類を固定する。
type browserEvent struct {
	Type     string  `json:"type"`
	Target   string  `json:"target"`
	Duration float64 `json:"durationMs"`
	Outcome  string  `json:"outcome"`
}

func (m *metrics) receive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var event browserEvent
	if err := decoder.Decode(&event); err != nil {
		http.Error(w, "invalid event", 400)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "one event required", 400)
		return
	}
	if math.IsNaN(event.Duration) || math.IsInf(event.Duration, 0) || event.Duration < 0 || event.Duration > 3600000 {
		http.Error(w, "invalid duration", 400)
		return
	}
	validAPI := event.Target == "/ok" || event.Target == "/slow" || event.Target == "/error" || event.Target == "/downstream"
	valid := false
	switch event.Type {
	case "click":
		valid = (validAPI || event.Target == "js-error") && event.Duration == 0 && event.Outcome == ""
	case "navigation":
		valid = event.Target == "/" && event.Outcome == ""
	case "fetch":
		valid = validAPI && (event.Outcome == "ok" || event.Outcome == "http_error" || event.Outcome == "network_error")
	case "js_error":
		valid = event.Target == "window" && event.Duration == 0 && event.Outcome == ""
	}
	if !valid {
		http.Error(w, "unsupported event", 400)
		return
	}
	option := metric.WithAttributes(attribute.String("target", event.Target))
	switch event.Type {
	case "click":
		m.clicks.Add(r.Context(), 1, option)
	case "navigation":
		m.navigation.Record(r.Context(), event.Duration, option)
	case "fetch":
		m.fetch.Record(r.Context(), event.Duration, option, metric.WithAttributes(attribute.String("outcome", event.Outcome)))
	case "js_error":
		m.jsErrors.Add(r.Context(), 1, option)
	}
	slog.Info("browser event", "type", event.Type, "target", event.Target, "duration_ms", event.Duration, "outcome", event.Outcome)
	w.WriteHeader(http.StatusNoContent)
}
