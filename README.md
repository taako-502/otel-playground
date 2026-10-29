# otel-playground

GoのHTTP APIでOpenTelemetryのトレースを学ぶ最小サンプル。
AWSアカウント、Docker、DBは不要です。

## 起動

Go 1.27以上を用意し、リポジトリのルートで実行します。

```sh
git clone https://github.com/taako-502/otel-playground.git
cd otel-playground
go run ./cmd
```

別のターミナルからアクセスします。

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
- `cmd/main_test.go`: HTTP応答と記録されたスパンのテスト。

トレースはリクエスト全体、スパンはその中の処理区間です。
このサンプルではトレースだけを扱い、メトリクスやクリック数、RUMはまだ収集しません。
出力は保存されず、ダッシュボードもありません。
次の段階ではOTLP Exporterと可視化用バックエンドを追加できます。

## テスト

リポジトリのルートで実行します。

```sh
go test ./...
```

HTTPハンドラーを呼び出し、応答、待機時間、有効なTraceID、
HTTPスパンと子スパンの親子関係、例外イベント、エラーステータスを確認します。
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
