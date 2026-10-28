# otel-playground

Node.jsのHTTP APIでOpenTelemetryのトレースを学ぶ最小サンプル。
AWSアカウント、Docker、DBは不要です。

## 起動

Node.js 22以上を用意します。

```sh
git clone https://github.com/taako-502/otel-playground.git
cd otel-playground
npm install
npm start
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
| /error | 意図的な例外でHTTP 500。例外イベントとERRORステータスを記録 |

レスポンスの traceId と、サーバーのターミナルに出る traceId を照合してください。
/slow のHTTPスパンと simulate-work は同じ traceId になります。
duration は [秒, ナノ秒] 形式です。/error の status.code: 2 はERRORを示します。
成功したスパンの code: 0 はUNSETで、異常という意味ではありません。
Ctrl+Cで停止します。ポート変更は `PORT=3001 npm start` です。

## 構成

- app.cjs: Node.js標準HTTPサーバー。/slowだけ手動で子スパンを作成。
- instrumentation.cjs: OpenTelemetryを初期化し、HTTPの自動計測を有効化。
- ConsoleSpanExporter: スパンをターミナルへ出力。
- SimpleSpanProcessor: 学習しやすいよう終了したスパンを即時出力。

`npm start` の --require で、HTTPモジュールより先に計測を初期化します。
`node app.cjs` で直接起動すると自動計測の読み込み順が崩れるため、npm startを使ってください。

トレースはリクエスト全体、スパンはその中の処理区間です。
このサンプルではトレースだけを扱い、メトリクスやクリック数、RUMはまだ収集しません。
出力は保存されず、ダッシュボードもありません。
次の段階ではOTLP Exporterと可視化用バックエンドを追加できます。

## テスト

```sh
npm test
```

実際にサーバーを起動し、3つの応答、待機時間、有効なtraceId、
トレース出力、子スパン、例外イベントを確認します。
GitHub Actionsでも実行します。

依存パッケージの直接バージョンを固定しています。
npm installで生成されるpackage-lock.jsonをコミットすれば推移的依存も固定できます。

## 参考

- [OpenTelemetry Node.js入門](https://opentelemetry.io/docs/languages/js/getting-started/nodejs/)
- [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/)

このサンプルはローカル学習用で、127.0.0.1でのみ待ち受けます。
