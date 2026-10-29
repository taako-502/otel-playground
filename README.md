# otel-playground

GoのHTTP APIとブラウザでOpenTelemetryのトレース・メトリクス・簡単なRUMを学ぶサンプル。
入門編はGoだけで実行できます。CollectorとGrafanaを使う実践編にはDocker Composeを使います。

## 起動

Go 1.27以上を用意し、リポジトリのルートで実行します。

```sh
git clone https://github.com/taako-502/otel-playground.git
cd otel-playground
go run ./cmd/api-a
```

上記はAPI A（操作画面と既存のAPI）です。別ターミナルでAPI Bも起動します。

```sh
go run ./cmd/api-b
```

API Aは `127.0.0.1:3000`、API Bは `127.0.0.1:3001` で待ち受けます。
ブラウザで http://127.0.0.1:3000 を開きます。
「正常なAPI」「1秒待つAPI」「HTTPエラー」「JavaScriptエラー」を押すと、
画面に結果が表示され、ターミナルにトレース・ブラウザイベント・メトリクスが出力されます。
Node.jsやnpmのインストールは不要です。

APIだけを試す場合は、別のターミナルからアクセスします。

```sh
curl http://127.0.0.1:3000/ok
curl http://127.0.0.1:3000/slow
curl -i http://127.0.0.1:3000/error
```

| API | 動作 |
| --- | --- |
| /ok | HTTP 200ですぐ応答 |
| /slow | 約1秒待ってHTTP 200。子スパン simulate-work を記録 |
| /error | 意図的なエラーでHTTP 500。例外イベントとERRORステータスを記録 |

レスポンスの `traceId` と、ターミナル出力の `SpanContext.TraceID` を照合してください。
`/slow` のHTTPスパンと `simulate-work` は同じTraceIDになります。
処理時間は `EndTime` と `StartTime` の差、エラーは `Status.Code: "Error"` で確認できます。
成功したスパンの `Status.Code: "Unset"` は、異常という意味ではありません。

Ctrl+Cで停止します。ポート変更は `PORT` で指定します。Bのポートを変える場合、Aの接続先も変更します。

```sh
# ターミナル1
PORT=3101 go run ./cmd/api-b
# ターミナル2
PORT=3100 API_B_URL=http://127.0.0.1:3101 go run ./cmd/api-a
```
このサンプルはローカル学習用で、127.0.0.1でのみ待ち受けます。

## 別サービスへのトレース伝播

画面の「API A → API B」でBの動作（正常・1秒待機・エラー）と伝播の有無を選び、「API Bを呼ぶ」を押します。
A単体の `/ok`・`/slow`・`/error` はBなしでも利用できます。

```sh
# 伝播あり：AとBは同じTraceID
curl 'http://127.0.0.1:3000/downstream?mode=slow&propagate=true'
# 伝播なし：Bは別のTraceIDで開始
curl 'http://127.0.0.1:3000/downstream?mode=slow&propagate=false'
# BがHTTP 500、AはHTTP 502で応答
curl -i 'http://127.0.0.1:3000/downstream?mode=error&propagate=true'
```

伝播ありでは、次の4スパンが同じTraceIDを共有し、それぞれ異なるSpanIDを持ちます。

```text
api-a: SERVER /downstream
└─ api-a: CLIENT → API B
   └─ api-b: SERVER /work
      └─ api-b: INTERNAL simulate-work
```

`otelhttp.NewTransport`がAのCLIENTスパンを作り、W3C Trace Context形式の`traceparent`をHTTPヘッダーへ注入します。
Aは`http.NewRequestWithContext(r.Context(), ...)`で受信リクエストのContextを引き継ぎます。
Bの`otelhttp.NewHandler`がそのヘッダーを読み取り、SERVERスパンの親にAのCLIENTスパンを設定します。
**Bの親はAのSERVERスパンではなく、AのCLIENTスパンです。**

画面とJSONレスポンスにはA/BのTraceID・SpanID、Bの親SpanID、Bが受信した`traceparent`を表示します。
両ターミナルの`Resource`内の`service.name`（`api-a` / `api-b`）、`SpanContext`、`Parent`と照合してください。
`traceparent`は`00-TraceID-送信側のSpanID-フラグ`という形式です。
Bが1秒待機した場合、Bの子スパンだけでなく、それを待つAのCLIENT・SERVERスパンの時間にも遅延が現れます。

伝播なしでは、送信用TransportのPropagatorを空にしてヘッダー注入だけを止めます。
ContextのキャンセルやA内の親子関係は維持されます。

```text
トレース1: api-a SERVER → api-a CLIENT
トレース2: api-b SERVER → api-b simulate-work
```

Bのエラー動作では、Bの子スパンとSERVERスパンに例外を記録します。
HTTP 500を受けたAのCLIENTスパンもErrorになり、AはHTTP 502を返してSERVERスパンをErrorにします。
B未起動・接続失敗・3秒のタイムアウトもAはHTTP 502で返します。
実サービスでのステータス変換はAPIの設計によります。ここでは下流サービスの失敗を区別する教材上の選択です。

この比較はA→Bの伝播が対象です。ブラウザ側のスパンはまだ追加していません。Collector・保存先・Grafanaは下記の実践編で追加できます。
画面はこのリポジトリ独自の学習用UIです。

## 構成

- `cmd/api-a/main.go`: HTTPサーバーとOpenTelemetryの初期化。`otelhttp`でHTTPハンドラーを計測。
- `cmd/api-a/downstream.go`: BへのHTTP通信。計測付きTransportと伝播あり／なしの比較。
- `cmd/api-b/main.go`: API Bの起動。
- `internal/work/handler.go`: Bの`/work`と手動子スパン。
- `internal/telemetry/`: サービス名ごとのSDK初期化とサーバー起動・終了処理。
- `/slow`: リクエストのContextから子スパン `simulate-work` を作成。
- `/error`: エラーを例外イベントとERRORステータスとして記録。
- `stdouttrace`と`SimpleSpanProcessor`: 終了したスパンをターミナルに即時出力。
- `cmd/api-a/metrics.go`: メトリクスの定義とブラウザイベントの受信・検証。
- `cmd/api-a/index.html`: Goバイナリに埋め込む操作画面とブラウザ用JavaScript。
- `cmd/api-a/*_test.go`: HTTP応答・スパン・メトリクス・不正イベントの検証。

トレースはリクエスト全体、スパンはその中の処理区間です。

## メトリクスとRUMの確認

メトリクスは約5秒ごとと正常終了時に標準出力へJSONで出力します。
`ScopeMetrics` 内の `Metrics` を探し、`Name` と `Data.DataPoints` を確認してください。
カウンターの `Value` は起動後の累積回数、ヒストグラムの `Count`・`Sum`・`BucketCounts` は件数・合計・分布です。
何も操作しない間も累積値を再出力するため、出力同士を足し合わせないでください。

| メトリクス | 内容 |
| --- | --- |
| `demo.api.requests` | GET `/ok`・`/slow`・`/error`・`/downstream` の呼び出し回数。`route`別 |
| `demo.api.errors` | 上記APIのHTTP 5xx回数。`route`別 |
| `http.server.request.duration` | Go側のHTTP処理時間（秒）。画面配信とイベント受信も含む |
| `demo.browser.clicks` | 操作ボタンのクリック回数。`target`別 |
| `demo.browser.errors` | ブラウザのJavaScriptエラー・未処理Promiseエラー回数 |
| `demo.browser.navigation.duration` | ページ読み込み開始からloadイベント終了までの時間（ms） |
| `demo.browser.fetch.duration` | ブラウザからAPIを呼び出し、本文を読み取るまでの時間（ms）。通信失敗時は失敗まで。`target`・`outcome`別 |

最初の4つのボタンを1回ずつ押すと、API呼び出しは3回、APIエラーは1回、クリックは4回、JavaScriptエラーは1回増えます。
「API Bを呼ぶ」もクリック・通信時間を`target=/downstream`として集計します。
`/slow`のブラウザ通信時間はおよそ1秒以上になります。ページを再読み込みすると読み込み時間を再計測します。
受信したイベントは `browser event` として標準エラー出力にも即時表示します。
画面の送信件数は、そのページからのイベント受信成功・失敗を示します。

RUM（実際のブラウザでの体験の計測）は、標準のPerformance APIとイベントリスナーを使った手動計測です。
ブラウザは計測値を独自形式で `POST /events` に送り、GoのOpenTelemetry SDKがメトリクスへ変換します。
OpenTelemetryのブラウザSDKやブラウザ側スパン、Core Web Vitals、セッション追跡は含みません。
画面に表示するTraceIDはAPIのサーバースパンのものです。
イベント送信自体の通信時間はブラウザ計測の対象に含めず、再帰的な送信を防いでいます。

入門編のターミナル出力は保存されず、サーバー再起動で集計をリセットします。実践編では送信済みのデータを保存できます。

## 実践編：Collector・保存先・Grafana

```text
Go API A / API B ── OTLP/HTTP ── Collector
                                  ├─ OTLP → Tempo（トレース保存）
                                  └─ Prometheus形式で公開 ← Prometheusが定期取得（メトリクス保存）
Grafana ── Tempo / Prometheusへ問い合わせて表示
```

### 起動

Docker Desktop等を起動して、リポジトリのルートで実行します。

```sh
docker compose up -d
```

A/Bを起動済みなら停止し、**両方**に送信先を指定して再起動してください。

```sh
# ターミナル1
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 go run ./cmd/api-b
# ターミナル2
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 go run ./cmd/api-a
```

- 操作画面：http://127.0.0.1:3000
- Grafana：http://127.0.0.1:3300/d/otel-playground （閲覧はログイン不要）
- Prometheus：http://127.0.0.1:9090
- Tempo API：http://127.0.0.1:3200

入門編と同じ標準出力を残し、トレースをバッチ送信、メトリクスを5秒周期で送信します。
Collectorがまとめて転送し、Prometheusが5秒周期で取得します。反映には通常10〜15秒程度待ってください。
環境変数を外して再起動すれば、外部サービスなしの入門編に戻ります。
`OTEL_EXPORTER_OTLP_ENDPOINT`が送信有効化のスイッチです。SDKの`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`等だけを指定しても有効にはしません。

### 遅延の原因を調べる演習

1. 「traceparentを伝播する」をONにする。
2. **Aが遅いケース**：Bを「正常」、**Aで1秒待機**をONにして実行する。
3. 結果欄の「このTraceIDをGrafanaで調べる」を開き、数秒後に再検索する。
4. Aの`prepare-work`が約1秒、その後のA CLIENTとB SERVERは短いことを確認する。
5. **Bが遅いケース**：Aの待機をOFF、Bを「1秒待機」にして実行する。
6. Bの`simulate-work`が約1秒で、その間A CLIENTとA SERVERも待っていることを確認する。

親スパンの時間は子スパンの時間を含みます。AとBの時間を足して合計処理時間と考えないでください。
Grafanaではサービス名`api-a`・`api-b`とスパンの横棒（ウォーターフォール）を見比べます。
SDKのバージョンによってHTTPスパン名は`GET`などになります。役割はSpan kind（SERVER/CLIENT）と属性でも確認できます。

Bを「エラー」にすると、B HTTP 500 → A HTTP 502になり、両サービスのエラースパンを調べられます。
伝播をOFFにするとAとBは別トレースです。BのTraceIDをダッシュボードのTraceID入力欄に貼り付けるとB側だけを表示できます。

### メトリクスを見る

ダッシュボードは自動登録されます。APIを繰り返し呼び、エラーも発生させると次を確認できます。

- API呼び出し累積回数（route別）
- APIの5xxエラー率（直近1分）
- HTTPサーバー処理時間の平均・p95（service別）
- クリック累積回数、ブラウザ通信時間の平均

エラーを一度も起こしていない場合、エラーの時系列は未作成なのでエラー率は`No data`です。
rateや平均には複数回の取得が必要です。初回起動後は10〜15秒待ってから追加操作してください。
p95はヒストグラムのバケットから計算した近似値です。各リクエストの正確な内訳はトレースを見ます。
HTTPメトリクスには操作画面と`/events`も含まれます。特定の処理だけの時間とは異なります。

画面のTraceIDは、ダッシュボード上部のTraceID入力欄へ貼り付けても下部に表示できます。
元のOTel名の`.`はPrometheus上では`_`になり、Counterには`_total`、時間には単位が付きます。
例：`demo.api.requests` → `demo_api_requests_total`。

### 保存・停止・接続確認

Composeの名前付きボリュームに保存します。Tempoの保持期間は24時間、Prometheusは7日間です。
`docker compose down`で停止してもボリュームは残り、次回起動時に保存済みデータを利用できます。
サンプルのカウンターはA/B再起動でリセットしますが、Prometheusの過去データは保持されます。
データも消して初期化したい場合だけ`docker compose down -v`を使います。

```sh
docker compose ps
docker compose logs --tail=30 collector tempo prometheus grafana
# 停止（GoのA/BはそれぞれCtrl+C）
docker compose down
```

ホストの公開ポートはすべて127.0.0.1に限定しています。認証なしの閲覧とHTTP通信を使うローカル学習用構成です。
本番に展開する場合は認証・TLS・アクセス制御・永続ストレージ・容量設計が別途必要です。
Tempoはこのローカル構成ではボリュームに書き込むためコンテナ内でroot実行しています。
Collectorの再送・キューはメモリ内で、停止中のデータを無制限に保持する構成ではありません。
標準出力への同期出力、全トレース収集も学習用の設定です。

ポート競合時は`GRAFANA_PORT`・`OTLP_PORT`・`TEMPO_PORT`・`PROMETHEUS_PORT`を指定してComposeを起動できます。
変更したOTLPポートをA/Bの送信先にも設定してください。画面のGrafanaリンクは既定の3300番用なので、変更時はGrafanaを直接開いてTraceIDを貼り付けます。

起動済みの全経路をGoの結合テストでも確認できます（約30秒）。
`integration`ビルドタグを指定したときだけ実行され、通常の`go test ./...`には含まれません。
実APIリクエストと検証用の合成クリック・通信時間イベントを送信するため、ダッシュボードの集計値に加算されます。

```sh
go test -tags=integration ./integration -v -count=1 -timeout=5m
# Aのポートを変えている場合
API_A_URL=http://127.0.0.1:3100 go test -tags=integration ./integration -v -count=1 -timeout=5m
```

Tempoの遅延スパン、Prometheusの全グラフ用クエリ、Grafana経由のトレース取得を検証します。
保存先ポートを変更した場合は`TEMPO_URL`・`PROMETHEUS_URL`・`GRAFANA_URL`も指定できます。

設定ファイルは`observability/`、ダッシュボード定義は`observability/grafana/dashboards/playground.json`です。
使用イメージのバージョンは`compose.yaml`で固定しています。

## テスト

リポジトリのルートで実行します。

```sh
go test ./...
```

HTTPハンドラーを呼び出し、応答、待機時間、有効なTraceID、
HTTPスパンと子スパンの親子関係、例外イベント、エラーステータス、メトリクス集計、不正イベントの拒否を確認します。
さらに、実HTTP通信でA→Bを呼び、伝播あり／なしそれぞれの正常・遅延・エラーをテストします。
TraceIDだけでなく、A SERVER → A CLIENT → B SERVER → 子スパンの親子関係とサービス名も検証します。
GitHub Actionsでも実行します。

## ビルド

各サービスの実行ファイルを `bin/` に作成します。

```sh
go build -o bin/api-a ./cmd/api-a
go build -o bin/api-b ./cmd/api-b
# 別々のターミナルで起動
./bin/api-b
./bin/api-a
```

## 参考

- [OpenTelemetry Go入門](https://opentelemetry.io/docs/languages/go/getting-started/)
- [Context propagation](https://opentelemetry.io/docs/concepts/context-propagation/)
- [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/)
