package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestMetricsAndBrowserEvents(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	measurements, err := newMetrics(provider)
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(noop.NewTracerProvider(), measurements, "http://127.0.0.1:3001")
	send := func(method, path, body string, status int) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		if response.Code != status {
			t.Fatalf("%s %s: status %d, body %s", method, path, response.Code, response.Body.String())
		}
	}
	send("GET", "/", "", 200)
	send("GET", "/ok", "", 200)
	send("GET", "/error", "", 500)
	for _, body := range []string{
		`{"type":"click","target":"/ok"}`,
		`{"type":"click","target":"js-error"}`,
		`{"type":"navigation","target":"/","durationMs":123}`,
		`{"type":"fetch","target":"/slow","durationMs":1001,"outcome":"ok"}`,
		`{"type":"fetch","target":"/error","durationMs":15,"outcome":"http_error"}`,
		`{"type":"fetch","target":"/ok","durationMs":25,"outcome":"network_error"}`,
		`{"type":"js_error","target":"window"}`,
	} {
		send("POST", "/events", body, 204)
	}
	send("GET", "/events", "", 405)
	for _, body := range []string{
		`{`, `{}`, `{"type":"click","target":"/unknown"}`,
		`{"type":"navigation","target":"/","durationMs":-1}`,
		`{"type":"navigation","target":"/","durationMs":3600001}`,
		`{"type":"fetch","target":"/ok","outcome":"arbitrary"}`,
		`{"type":"click","target":"/ok","extra":1}`,
		`{"type":"click","target":"/ok"} {}`,
		`{"type":"click","target":"` + strings.Repeat("a", 2100) + `"}`,
	} {
		send("POST", "/events", body, 400)
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	counters := map[string]int64{}
	counts := map[string]uint64{}
	sums := map[string]float64{}
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			switch points := metric.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range points.DataPoints {
					counters[metric.Name] += point.Value
				}
			case metricdata.Histogram[float64]:
				for _, point := range points.DataPoints {
					counts[metric.Name] += point.Count
					sums[metric.Name] += point.Sum
				}
			}
		}
	}
	for name, want := range map[string]int64{"demo.api.requests": 2, "demo.api.errors": 1, "demo.browser.clicks": 2, "demo.browser.errors": 1} {
		if counters[name] != want {
			t.Errorf("%s = %d, want %d", name, counters[name], want)
		}
	}
	if counts["demo.browser.navigation.duration"] != 1 || sums["demo.browser.navigation.duration"] != 123 {
		t.Error("navigation measurement missing")
	}
	if counts["demo.browser.fetch.duration"] != 3 || sums["demo.browser.fetch.duration"] != 1041 {
		t.Error("fetch measurements missing")
	}
	// HTTP計測には画面配信とイベント受信も含まれる。
	if counts["http.server.request.duration"] != 20 {
		t.Error("HTTP duration histogram missing")
	}
}
