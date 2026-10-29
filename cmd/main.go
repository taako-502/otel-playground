package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return err
	}
	// 学習用に、終了したスパンを即時出力する。
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "otel-playground"))),
	)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		return err
	}
	server := &http.Server{Handler: newHandler(provider), ReadHeaderTimeout: 5 * time.Second}
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

func newHandler(provider trace.TracerProvider) http.Handler {
	tracer := provider.Tracer("playground")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		reply := func(status int, body map[string]any) {
			body["traceId"] = span.SpanContext().TraceID().String()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(body)
		}
		if r.Method != http.MethodGet {
			reply(http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
			return
		}
		switch r.URL.Path {
		case "/ok":
			reply(http.StatusOK, map[string]any{"message": "ok"})
		case "/slow":
			// リクエストのContextを渡し、HTTPスパンの子として記録する。
			_, child := tracer.Start(r.Context(), "simulate-work")
			child.SetAttributes(attribute.Int("demo.delay_ms", 1000))
			time.Sleep(time.Second)
			child.End()
			reply(http.StatusOK, map[string]any{"message": "slow", "delayMs": 1000})
		case "/error":
			err := errors.New("Intentional demo error")
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			reply(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		default:
			reply(http.StatusNotFound, map[string]any{"error": "Use /ok, /slow, or /error"})
		}
	})
	return otelhttp.NewHandler(handler, "HTTP request",
		otelhttp.WithTracerProvider(provider),
		otelhttp.WithPropagators(propagation.TraceContext{}),
	)
}
