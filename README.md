# otel-playground

GoのHTTP APIとブラウザでOpenTelemetryのトレース・メトリクス・簡単なRUMを学ぶサンプル。
AWSアカウント、Docker、DBは不要です。

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

この比較はA→Bの伝播が対象です。ブラウザ側のスパンや、Collector・外部可視化バックエンドはまだ追加していません。
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

出力は保存されず、サーバー再起動で集計をリセットします。外部ダッシュボードは不要です。
次の段階ではOTLP Exporterと可視化用バックエンドを追加できます。

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
