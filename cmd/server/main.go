package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/starcs/star-launcher-backend/internal/api"
	"github.com/starcs/star-launcher-backend/internal/demo"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
)

const defaultOrigins = "http://localhost:1420,http://tauri.localhost,https://tauri.localhost,tauri://localhost"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	addr := envOrDefault("STAR_BACKEND_ADDR", ":8080")
	origins := strings.Split(envOrDefault("STAR_CORS_ORIGINS", defaultOrigins), ",")

	var players api.PlayerRepository
	dsn := strings.TrimSpace(os.Getenv("STAR_DB_DSN"))
	if dsn == "" {
		logger.Warn("STAR_DB_DSN is not configured; real login and inventory are unavailable")
	} else {
		repository, err := mysqlrepo.Open(context.Background(), dsn)
		if err != nil {
			logger.Error("connect inventory database", "error", err)
			os.Exit(1)
		}
		defer repository.Close()
		players = repository
		logger.Info("real inventory database connected")
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           api.NewHandler(demo.NewStore(), players, logger, origins),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-shutdownSignal.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("STAR launcher backend started", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
	logger.Info("STAR launcher backend stopped")
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
