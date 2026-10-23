package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudrifts/panel/internal/api"
	"github.com/cloudrifts/panel/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.LoadDefault()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewRouter(cfg, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("cloudriftsd starting", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("cloudriftsd failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger.Info("cloudriftsd stopping")
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("cloudriftsd shutdown failed", "error", err)
		os.Exit(1)
	}
}
