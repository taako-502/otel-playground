package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPExportAndShutdown(t *testing.T) {
	var mu sync.Mutex
	var traceSeen, metricSeen bool
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/traces":
			var request tracepb.ExportTraceServiceRequest
			if err := proto.Unmarshal(data, &request); err != nil {
				t.Error(err)
			}
			for _, resource := range request.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						if span.Name == "export-test" {
							traceSeen = true
						}
					}
				}
			}
		case "/v1/metrics":
			var request metricspb.ExportMetricsServiceRequest
			if err := proto.Unmarshal(data, &request); err != nil {
				t.Error(err)
			}
			for _, resource := range request.ResourceMetrics {
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						if metric.Name == "test.counter" {
							metricSeen = true
						}
					}
				}
			}
		default:
			t.Errorf("unexpected OTLP path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", receiver.URL+"/v1/traces")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", receiver.URL+"/v1/metrics")
	p, shutdown, err := New("test-service")
	if err != nil {
		t.Fatal(err)
	}
	_, span := p.Traces.Tracer("test").Start(context.Background(), "export-test")
	span.End()
	counter, err := p.Metrics.Meter("test").Int64Counter("test.counter")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1)
	// 周期を待たず、終了時にバッチと最後のメトリクスが送られることを検証。
	shutdown()
	mu.Lock()
	defer mu.Unlock()
	if !traceSeen || !metricSeen {
		t.Fatalf("OTLP received traces=%v metrics=%v", traceSeen, metricSeen)
	}
}
