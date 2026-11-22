// Command mockprovider serves the mock OpenAI/Anthropic/Gemini APIs from
// internal/mockllm for local development. It never calls real providers.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"service/internal/mockllm"
)

func main() {
	addr := os.Getenv("MOCK_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	delay := 25 * time.Millisecond
	if v, err := time.ParseDuration(os.Getenv("MOCK_CHUNK_DELAY")); err == nil {
		delay = v
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("mock provider listening", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: mockllm.Handler(delay), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
