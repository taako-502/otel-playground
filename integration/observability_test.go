//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 起動済みのA/B・Collector・Tempo・Prometheus・Grafanaを通して検証する。
// 実リクエストと合成ブラウザイベントを送るため、保存先の集計値に加算される。
func TestObservability(t *testing.T) {
	a := endpoint("API_A_URL", "http://127.0.0.1:3000")
	tempo := endpoint("TEMPO_URL", "http://127.0.0.1:3200")
	prom := endpoint("PROMETHEUS_URL", "http://127.0.0.1:9090")
	grafana := endpoint("GRAFANA_URL", "http://127.0.0.1:3300")
	client := &http.Client{Timeout: 10 * time.Second}
	ctx := t.Context()

	traffic := func() map[string]string {
		t.Helper()
		ids := make(map[string]string)
		for _, tc := range []struct {
			name, query string
			status      int
		}{
			{"a", "mode=ok&delay_a=true", 200},
			{"b", "mode=slow", 200},
			{"error", "mode=error", 502},
		} {
			var body struct {
				TraceID string `json:"traceId"`
			}
			if err := request(ctx, client, a+"/downstream?"+tc.query, nil, tc.status, &body); err != nil {
				t.Fatal(err)
			}
			_, err := hex.DecodeString(body.TraceID)
			if len(body.TraceID) != 32 || err != nil || body.TraceID == strings.Repeat("0", 32) {
				t.Fatalf("invalid trace ID: %q", body.TraceID)
			}
			ids[tc.name] = body.TraceID
		}
		for _, event := range []map[string]any{
			{"type": "click", "target": "/downstream"},
			{"type": "fetch", "target": "/downstream", "durationMs": 1000, "outcome": "ok"},
		} {
			if err := request(ctx, client, a+"/events", event, 204, nil); err != nil {
				t.Fatal(err)
			}
		}
		return ids
	}
	t.Log("実リクエストと検証用の合成ブラウザイベントを送信します")
	ids := traffic()
	// 初回のメトリクス送信とPrometheus取得が済んでから増分を作る。
	if err := pause(ctx, 12*time.Second); err != nil {
		t.Fatal(err)
	}
	traffic()

	for _, tc := range []struct {
		name, span string
		count      int
	}{
		{"a", "prepare-work", 5}, {"b", "simulate-work", 4},
	} {
		t.Run("Tempo/"+tc.name, func(t *testing.T) {
			eventually(t, func(ctx context.Context) error {
				var data tempoTrace
				if err := request(ctx, client, tempo+"/api/traces/"+ids[tc.name], nil, 200, &data); err != nil {
					return err
				}
				batches := data.Batches
				if len(batches) == 0 {
					batches = data.ResourceSpans
				}
				count := 0
				found := false
				for _, batch := range batches {
					for _, scope := range batch.ScopeSpans {
						for _, span := range scope.Spans {
							count++
							if span.Name == tc.span {
								start, err := span.Start.Int64()
								if err != nil {
									return err
								}
								end, err := span.End.Int64()
								if err != nil {
									return err
								}
								found = end-start >= int64(950*time.Millisecond)
							}
						}
					}
				}
				if !found || count != tc.count {
					return fmt.Errorf("%s: delayed span=%v, count=%d (want %d)", tc.span, found, count, tc.count)
				}
				return nil
			})
		})
	}

	var dashboard struct {
		Dashboard struct {
			Panels []struct {
				Title   string `json:"title"`
				Type    string `json:"type"`
				Targets []struct {
					Expr string `json:"expr"`
				} `json:"targets"`
			} `json:"panels"`
		} `json:"dashboard"`
	}
	if err := request(ctx, client, grafana+"/api/dashboards/uid/otel-playground", nil, 200, &dashboard); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, panel := range dashboard.Dashboard.Panels {
		if panel.Type != "timeseries" {
			continue
		}
		checked++
		t.Run("Prometheus/"+panel.Title, func(t *testing.T) {
			if len(panel.Targets) == 0 {
				t.Fatal("missing panel query")
			}
			for _, target := range panel.Targets {
				if target.Expr == "" {
					t.Fatal("empty panel query")
				}
				eventually(t, func(ctx context.Context) error {
					var result struct {
						Status string `json:"status"`
						Data   struct {
							Result []struct {
								Value []json.RawMessage `json:"value"`
							} `json:"result"`
						} `json:"data"`
					}
					query := url.Values{"query": {target.Expr}}.Encode()
					if err := request(ctx, client, prom+"/api/v1/query?"+query, nil, 200, &result); err != nil {
						return err
					}
					if result.Status != "success" {
						return fmt.Errorf("Prometheus status: %s", result.Status)
					}
					for _, row := range result.Data.Result {
						if len(row.Value) != 2 {
							continue
						}
						var raw string
						if json.Unmarshal(row.Value[1], &raw) != nil {
							continue
						}
						value, err := strconv.ParseFloat(raw, 64)
						if err == nil && !math.IsNaN(value) && !math.IsInf(value, 0) {
							return nil
						}
					}
					return fmt.Errorf("no finite samples for %s", target.Expr)
				})
			}
		})
	}
	if checked == 0 {
		t.Fatal("dashboard contains no metric panels")
	}
	t.Run("Grafana/trace", func(t *testing.T) {
		body := map[string]any{
			"queries": []any{map[string]any{"refId": "A", "datasource": map[string]string{"type": "tempo", "uid": "tempo"}, "queryType": "traceId", "query": ids["a"], "filters": []any{}}},
			"from":    "now-15m", "to": "now",
		}
		var response struct {
			Results map[string]struct {
				Status int               `json:"status"`
				Frames []json.RawMessage `json:"frames"`
			} `json:"results"`
		}
		if err := request(ctx, client, grafana+"/api/ds/query", body, 200, &response); err != nil {
			t.Fatal(err)
		}
		result := response.Results["A"]
		if result.Status != 200 || len(result.Frames) == 0 {
			t.Fatalf("Grafana trace response: status=%d frames=%d", result.Status, len(result.Frames))
		}
	})
	t.Log(grafana + "/d/otel-playground?viewPanel=8&var-traceId=" + ids["a"])
}

type tempoBatch struct {
	ScopeSpans []struct {
		Spans []struct {
			Name  string      `json:"name"`
			Start json.Number `json:"startTimeUnixNano"`
			End   json.Number `json:"endTimeUnixNano"`
		} `json:"spans"`
	} `json:"scopeSpans"`
}
type tempoTrace struct {
	Batches       []tempoBatch `json:"batches"`
	ResourceSpans []tempoBatch `json:"resourceSpans"`
}

func endpoint(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return strings.TrimRight(value, "/")
	}
	return fallback
}

func request(ctx context.Context, client *http.Client, endpoint string, body any, wantStatus int, dst any) error {
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		return fmt.Errorf("%s %s: status %d, want %d", method, endpoint, response.StatusCode, wantStatus)
	}
	if dst == nil {
		_, err = io.Copy(io.Discard, response.Body)
		return err
	}
	if err = json.NewDecoder(response.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

func eventually(t *testing.T, check func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	for {
		err := check(ctx)
		if err == nil {
			return
		}
		if pause(ctx, time.Second) != nil {
			t.Fatalf("確認がタイムアウトしました: %v", err)
		}
	}
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
