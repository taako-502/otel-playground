# otel-playground

GoのHTTP APIとブラウザでOpenTelemetryのトレース・メトリクス・簡単なRUMを学ぶサンプル。
AWSアカウント、Docker、DBは不要です。

## 起動

Go 1.27以上を用意し、リポジトリのルートで実行します。

```sh
git clone https://github.com/taako-502/otel-playground.git
cd otel-playground
go run ./cmd
```

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

Ctrl+Cで停止します。ポート変更は `PORT=3001 go run ./cmd` です。
このサンプルはローカル学習用で、127.0.0.1でのみ待ち受けます。

## 構成

- `cmd/main.go`: HTTPサーバーとOpenTelemetryの初期化。`otelhttp`でHTTPハンドラーを計測。
- `/slow`: リクエストのContextから子スパン `simulate-work` を作成。
- `/error`: エラーを例外イベントとERRORステータスとして記録。
- `stdouttrace`と`SimpleSpanProcessor`: 終了したスパンをターミナルに即時出力。
- `cmd/metrics.go`: メトリクスの定義とブラウザイベントの受信・検証。
- `cmd/index.html`: Goバイナリに埋め込む操作画面とブラウザ用JavaScript。
- `cmd/*_test.go`: HTTP応答・スパン・メトリクス・不正イベントの検証。

トレースはリクエスト全体、スパンはその中の処理区間です。

## メトリクスとRUMの確認

メトリクスは約5秒ごとと正常終了時に標準出力へJSONで出力します。
`ScopeMetrics` 内の `Metrics` を探し、`Name` と `Data.DataPoints` を確認してください。
カウンターの `Value` は起動後の累積回数、ヒストグラムの `Count`・`Sum`・`BucketCounts` は件数・合計・分布です。
何も操作しない間も累積値を再出力するため、出力同士を足し合わせないでください。

| メトリクス | 内容 |
| --- | --- |
| `demo.api.requests` | GET `/ok`・`/slow`・`/error` の呼び出し回数。`route`別 |
| `demo.api.errors` | 上記APIのHTTP 500回数。`route`別 |
| `http.server.request.duration` | Go側のHTTP処理時間（秒）。画面配信とイベント受信も含む |
| `demo.browser.clicks` | 操作ボタンのクリック回数。`target`別 |
| `demo.browser.errors` | ブラウザのJavaScriptエラー・未処理Promiseエラー回数 |
| `demo.browser.navigation.duration` | ページ読み込み開始からloadイベント終了までの時間（ms） |
| `demo.browser.fetch.duration` | ブラウザからAPIを呼び出し、本文を読み取るまでの時間（ms）。通信失敗時は失敗まで。`target`・`outcome`別 |

4つのボタンを1回ずつ押すと、API呼び出しは3回、APIエラーは1回、クリックは4回、JavaScriptエラーは1回増えます。
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
GitHub Actionsでも実行します。

## ビルド

`cmd/`ディレクトリと出力名が衝突しないよう指定します。

```sh
go build -o bin/otel-playground ./cmd
./bin/otel-playground
```

## 参考

- [OpenTelemetry Go入門](https://opentelemetry.io/docs/languages/go/getting-started/)
- [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/)
