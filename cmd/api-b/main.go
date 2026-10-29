package main

import (
	"github.com/taako-502/otel-playground/internal/telemetry"
	"github.com/taako-502/otel-playground/internal/work"
	"log"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	providers, shutdown, err := telemetry.New("api-b")
	if err != nil {
		return err
	}
	defer shutdown()
	return telemetry.Serve(work.NewHandler(providers.Traces, providers.Metrics), "3001")
}
