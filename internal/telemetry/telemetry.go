// Package telemetry provides the shared local-demo initialization for both services.
package telemetry

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Providers struct {
	Traces  *sdktrace.TracerProvider
	Metrics *sdkmetric.MeterProvider
}

func New(service string) (Providers, func(), error) {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return Providers{}, nil, err
	}
	metricExporter, err := stdoutmetric.New()
	if err != nil {
		_ = exporter.Shutdown(context.Background())
		return Providers{}, nil, err
	}
	res := resource.NewSchemaless(attribute.String("service.name", service))
	traceOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(res), sdktrace.WithSyncer(exporter)}
	metricOptions := []sdkmetric.Option{sdkmetric.WithResource(res), sdkmetric.WithReader(
		sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(5*time.Second)))}
	// エンドポイント未指定なら従来どおりターミナルだけで学習できる。
	// HTTP/TLS、ヘッダーなどはOTel標準の環境変数をExporterが読み取る。
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		remoteTraces, err := otlptracehttp.New(ctx)
		if err != nil {
			_ = exporter.Shutdown(ctx)
			_ = metricExporter.Shutdown(ctx)
			return Providers{}, nil, err
		}
		remoteMetrics, err := otlpmetrichttp.New(ctx)
		if err != nil {
			_ = remoteTraces.Shutdown(ctx)
			_ = exporter.Shutdown(ctx)
			_ = metricExporter.Shutdown(ctx)
			return Providers{}, nil, err
		}
		traceOptions = append(traceOptions, sdktrace.WithBatcher(remoteTraces, sdktrace.WithBatchTimeout(time.Second)))
		metricOptions = append(metricOptions, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(remoteMetrics, sdkmetric.WithInterval(5*time.Second))))
	}
	p := Providers{Traces: sdktrace.NewTracerProvider(traceOptions...), Metrics: sdkmetric.NewMeterProvider(metricOptions...)}
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.Metrics.Shutdown(ctx); err != nil {
			log.Print(err)
		}
		if err := p.Traces.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}
	return p, shutdown, nil
}

func Serve(handler http.Handler, defaultPort string) error {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Println("Listening on http://" + listener.Addr().String())
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
