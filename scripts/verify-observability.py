#!/usr/bin/env python3
"""起動済みA/B・Collector・保存先・Grafanaを通した実践編の動作確認。
APIの正常・遅延・エラー操作と、検証用の合成ブラウザイベントを送信する。
"""
import json
import math
import os
import time
import urllib.error
import urllib.parse
import urllib.request

A = os.environ.get("API_A_URL", "http://127.0.0.1:3000")
TEMPO = os.environ.get("TEMPO_URL", "http://127.0.0.1:3200")
PROM = os.environ.get("PROMETHEUS_URL", "http://127.0.0.1:9090")
GRAFANA = os.environ.get("GRAFANA_URL", "http://127.0.0.1:3300")


def request(url, body=None, expected=200):
    req = urllib.request.Request(url, data=None if body is None else json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json", "Accept": "application/json"})
    try:
        response = urllib.request.urlopen(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    assert response.status == expected, (url, response.status, response.read().decode())
    raw = response.read()
    return json.loads(raw) if raw else None


def eventually(check, timeout=45):
    deadline = time.monotonic() + timeout
    while True:
        try:
            return check()
        except (AssertionError, urllib.error.URLError, KeyError) as error:
            if time.monotonic() >= deadline:
                raise RuntimeError(f"確認がタイムアウトしました: {error}") from error
            time.sleep(1)


def traffic():
    result = {}
    for key, query, status in [("a", "mode=ok&delay_a=true", 200),
                               ("b", "mode=slow", 200), ("error", "mode=error", 502)]:
        data = request(A + "/downstream?" + query, expected=status)
        result[key] = data["traceId"]
    for event in [{"type": "click", "target": "/downstream"},
                  {"type": "fetch", "target": "/downstream", "durationMs": 1000, "outcome": "ok"}]:
        request(A + "/events", event, expected=204)
    return result


print("実リクエストと検証用の合成ブラウザイベントを送信します。", flush=True)
ids = traffic()
time.sleep(12)  # 初回のメトリクスを保存してから増分を作る。
traffic()


def check_trace(trace_id, expected_name):
    data = request(TEMPO + "/api/traces/" + trace_id)
    spans = [span for batch in data.get("batches", data.get("resourceSpans", []))
             for scope in batch.get("scopeSpans", []) for span in scope["spans"]]
    slow = [span for span in spans if span["name"] == expected_name]
    assert slow and int(slow[0]["endTimeUnixNano"]) - int(slow[0]["startTimeUnixNano"]) >= 950_000_000
    assert len(spans) == (5 if expected_name == "prepare-work" else 4)


for case, span in [("a", "prepare-work"), ("b", "simulate-work")]:
    eventually(lambda: check_trace(ids[case], span))
    print(f"Tempo: {span}の約1秒の遅延を確認しました。", flush=True)

dashboard = request(GRAFANA + "/api/dashboards/uid/otel-playground")["dashboard"]
for panel in dashboard["panels"]:
    if panel["type"] != "timeseries":
        continue
    expression = panel["targets"][0]["expr"]

    def check_metric():
        result = request(PROM + "/api/v1/query?" + urllib.parse.urlencode({"query": expression}))["data"]["result"]
        assert result and any(math.isfinite(float(row["value"][1])) for row in result), panel["title"]

    eventually(check_metric)
    print("Prometheus:", panel["title"], "OK", flush=True)

frames = request(GRAFANA + "/api/ds/query", {
    "queries": [{"refId": "A", "datasource": {"type": "tempo", "uid": "tempo"},
                 "queryType": "traceId", "query": ids["a"], "filters": []}],
    "from": "now-15m", "to": "now",
})["results"]["A"]
assert frames["status"] == 200 and frames["frames"]
print("Grafana経由のトレース取得も成功しました。")
print(GRAFANA + "/d/otel-playground?viewPanel=8&var-traceId=" + ids["a"])
