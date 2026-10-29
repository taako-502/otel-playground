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
	p := Providers{
		// 終了直後に観察するため同期出力。本番のバッチ送信は次の学習段階。
		Traces: sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSyncer(exporter)),
		Metrics: sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(5*time.Second)))),
	}
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
