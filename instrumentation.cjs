const { NodeSDK } = require('@opentelemetry/sdk-node');
const { ConsoleSpanExporter, SimpleSpanProcessor } = require('@opentelemetry/sdk-trace-base');
const { HttpInstrumentation } = require('@opentelemetry/instrumentation-http');

// HTTPモジュールより先に読み込み、自動計測を有効にする。
// 学習用に各スパンを即時出力する。本番では通常バッチ送信を使う。
const sdk = new NodeSDK({
  serviceName: 'otel-playground',
  spanProcessors: [new SimpleSpanProcessor(new ConsoleSpanExporter())],
  instrumentations: [new HttpInstrumentation()],
});
sdk.start();
module.exports = sdk;
