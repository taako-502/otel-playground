# otel-playground

GoのAPIとブラウザを操作しながら、OpenTelemetryのトレース・メトリクスを学ぶサンプルです。
独自の学習用UIから正常・遅延・エラーを発生させ、Grafanaで原因や傾向を確認できます。

## まずは用語だけ

| 用語 | 簡単にいうと |
| --- | --- |
| API / UI | APIはプログラム同士のやり取りの窓口、UIは人がボタンなどで操作する画面です。 |
| OpenTelemetry（OTel） | 処理時間や回数などを、共通の仕組みで計測・送信するための道具です。 |
| トレース / スパン | トレースは1回の処理の道筋、スパンはその中の一区間です。「AがBを呼ぶ」のどこで時間がかかったかを追えます。 |
| TraceID / 伝播 | TraceIDはトレースの識別番号。別サービスにも引き継ぐ「伝播」によって、AとBの処理をひとつにつなげます。`traceparent`はその情報を運ぶHTTPヘッダーです。 |
| メトリクス | 呼び出し回数や処理時間などの数値です。時間ごとの増減や傾向を見るのに使います。 |
| Collector / OTLP | Collectorは計測データの中継役、OTLPはそのデータを送るための通信ルールです。 |
| Tempo | トレースを保存・検索するサーバーソフトの製品名です。 |
| Prometheus | 数値を時刻とセットで保存するデータベースと、数値を定期的に集める機能を備えた監視サーバーソフトです。 |
| Grafana / ダッシュボード | Grafanaは保存されたデータをグラフなどで見る道具。グラフをまとめた画面をダッシュボードと呼びます。 |
| RUM | 実際のブラウザで、ページの読み込み時間や操作時のエラーなどを測ることです。 |
| p95 / エラー率 | p95は「100回中およそ95回がこの時間以内に終わる」という目安。エラー率は呼び出しのうち失敗した割合です。 |
| Docker Compose | CollectorやGrafanaなど、複数のソフトをまとめて起動・停止する道具です。 |

このサンプルでは、**OpenTelemetryで測る → Collectorが中継する → Tempo・Prometheusに保存する → Grafanaで見る**、という流れです。

このサンプルでは、`docker compose up -d`で**Collector・Tempo・Prometheus・Grafanaの4つのソフト**を起動し、GoのAPI A/Bは別に起動します。

## 起動する

Go 1.27以上とDocker Composeを用意します。リポジトリのルートで保存・可視化用サービスを起動します。

```sh
docker compose up -d
```

続いて、別々のターミナルでAPI BとAPI Aを起動します。

```sh
# ターミナル1：API B
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 go run ./cmd/api-b
```

```sh
# ターミナル2：API Aと操作画面
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 go run ./cmd/api-a
```

[操作画面](http://127.0.0.1:3000)を開けば、以降の操作はブラウザで行えます。
[Grafanaダッシュボード](http://127.0.0.1:3300/d/otel-playground)はログイン不要です。
[Prometheusの検索画面](http://127.0.0.1:9090)では、保存されたメトリクスを直接確認できます。
例えば、操作画面でAPIを呼び出した後、`demo_api_requests_total`を検索するとAPIの呼び出し回数を確認できます（反映まで10〜15秒程度）。

## 画面で試す

「正常なAPI」「1秒待つAPI」「HTTPエラー」「JavaScriptエラー」で、応答やエラーを確認します。
「API A → API B」では、サービスをまたぐ処理を試せます。

| 試すこと | 画面での操作 | 確認すること |
| --- | --- | --- |
| トレースの伝播 | 「traceparentを伝播する」をONにして「API Bを呼ぶ」 | AとBが同じTraceIDになる |
| 伝播しない場合 | 伝播をOFFにして実行 | AとBが別のTraceIDになる |
| Aの遅延 | Bを「正常」、Aで1秒待機をON | Aの`prepare-work`が約1秒かかる |
| Bの遅延 | Bを「1秒待機」、Aの待機をOFF | Bの`simulate-work`が約1秒かかり、Aも待つ |
| 下流のエラー | Bを「エラー」にする | BがHTTP 500、AがHTTP 502を返す |

遅延やエラーの詳細は、結果欄の「このTraceIDをGrafanaで調べる」から確認します。
データがまだ表示されない場合は、数秒待って再読み込みしてください。
伝播OFF時のB側を見るには、BのTraceIDをGrafanaのTraceID欄に貼り付けます。

### トレースの見方

トレースは一連の処理、スパンはその中の処理区間です。伝播ONでは、次のスパンが同じTraceIDでつながります。

```text
api-a: SERVER /downstream
└─ api-a: CLIENT → API B
   └─ api-b: SERVER /work
      └─ api-b: INTERNAL simulate-work
```

AのHTTPクライアントが`traceparent`ヘッダーを送り、Bが受け取ることで親子関係を引き継ぎます。
Grafanaではサービス名とスパンの横棒を見比べると、AとBのどこで時間がかかったかを調べられます。
**親スパンの時間には子スパンの時間も含まれるため、足し合わせないでください。**

### メトリクスの見方

Grafanaダッシュボードには、API呼び出し回数・5xxエラー率・HTTP処理時間の平均とp95・クリック回数・ブラウザ通信時間の平均を表示します。
ボタンを何度か押し、10〜15秒ほど待って表示を確認してください。

- エラーを一度も発生させていない場合、エラー率は`No data`になります。
- 平均やエラー率には複数回のデータ取得が必要です。初回の操作から少し待って、追加で操作してください。
- p95は分布から求めた近似値です。個々の処理の詳細はトレースで確認します。
- HTTP処理時間には、操作画面の配信やブラウザイベントの受信も含みます。

## データの流れ

```text
ブラウザ ── 操作・簡易RUM ── API A ── HTTP / traceparent ── API B
                              │                              │
                              └────── OTLP/HTTP ─────────────┘
                                         ↓
                                     Collector
                                     ├─ Tempo（トレース保存）
                                     └─ Prometheus（メトリクス取得・保存）
                                         ↑
                               Grafanaで両方を表示
```

ブラウザのクリック・JavaScriptエラー・読み込み時間・通信時間は、Performance APIなどで手動計測し、API AのOpenTelemetry SDKでメトリクスに変換します。
これは簡易RUMで、ブラウザ側のスパンやCore Web Vitals、セッション追跡は含みません。画面のTraceIDはAPI側のものです。

ローカル学習用の構成です。本番で使う際は認証・TLS・アクセス制御・保存容量・サンプリングなどを別途設計してください。

## 停止する

API A/Bは各ターミナルでCtrl+C、保存・可視化用サービスは次で停止します。

```sh
docker compose down
```

保存データは残ります（トレースは24時間、メトリクスは7日間保持）。
API再起動でカウンターはリセットされますが、保存済みの履歴は残ります。
データも消して初期化する場合だけ、`docker compose down -v`を使います。

<details>
<summary>起動オプション・ポートの変更</summary>

- **Goだけで試す場合**：Docker Composeは不要です。送信先の環境変数を付けずに`go run ./cmd/api-b`と`go run ./cmd/api-a`を実行します。操作画面とターミナル出力で確認でき、Grafanaでの表示・保存は行いません。
- **APIのポート**：`PORT`で変更できます（Aは3000、Bは3001）。Bを変更したら、Aに`API_B_URL=http://127.0.0.1:変更後のポート`も設定します。
- **保存・可視化側のポート**：Composeの`GRAFANA_PORT`・`OTLP_PORT`・`TEMPO_PORT`・`PROMETHEUS_PORT`で変更できます。OTLPを変更したらA/Bの送信先も変更してください。画面のGrafanaリンクは3300番固定なので、変更時はGrafanaを直接開き、TraceIDを貼り付けます。
- **送信の有効化**：A/Bの両方に`OTEL_EXPORTER_OTLP_ENDPOINT`を指定します。信号別の送信先環境変数だけでは有効になりません。

</details>

<details>
<summary>開発者向け：コードとテスト</summary>

| 場所 | 内容 |
| --- | --- |
| `cmd/api-a/` | 操作画面、API、BへのHTTP通信、ブラウザイベントの計測 |
| `cmd/api-b/`・`internal/work/` | API Bと正常・遅延・エラー処理 |
| `internal/telemetry/` | OpenTelemetry SDKの初期化と終了処理 |
| `observability/` | Collector・保存先・Grafanaの設定 |
| `integration/` | 起動中のサービスを使う結合テスト |

通常のテストは外部サービスなしで実行できます。

```sh
go test ./...
```

A/BとComposeを起動した状態で、トレース保存・メトリクス・Grafana経由の取得をまとめて検証できます。
検証用のリクエストや合成ブラウザイベントも集計値に加算されます。

```sh
go test -tags=integration ./integration -v -count=1 -timeout=5m
```

接続先を変更した場合は`API_A_URL`・`TEMPO_URL`・`PROMETHEUS_URL`・`GRAFANA_URL`で指定します。

</details>

## 参考

- [OpenTelemetry Go入門](https://opentelemetry.io/docs/languages/go/getting-started/)
- [Context propagation](https://opentelemetry.io/docs/concepts/context-propagation/)
- [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/)
