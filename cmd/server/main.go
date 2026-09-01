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
	"github.com/starcs/star-launcher-backend/internal/clientprefs"
	"github.com/starcs/star-launcher-backend/internal/config"
	"github.com/starcs/star-launcher-backend/internal/demo"
	"github.com/starcs/star-launcher-backend/internal/gamews"
	"github.com/starcs/star-launcher-backend/internal/mysqlrepo"
	"github.com/starcs/star-launcher-backend/internal/steamgroup"
)

const defaultOrigins = "http://localhost:1420,http://tauri.localhost,https://tauri.localhost,tauri://localhost"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	envPath, err := config.LoadDotEnv()
	if err != nil {
		logger.Error("load .env", "error", err)
		os.Exit(1)
	}
	if envPath != "" {
		logger.Info("environment file loaded", "path", envPath)
	}
	addr := envOrDefault("STAR_BACKEND_ADDR", ":8080")
	origins := strings.Split(envOrDefault("STAR_CORS_ORIGINS", defaultOrigins), ",")
	skipPasswordAuth, err := config.SkipPasswordAuth()
	if err != nil {
		logger.Error("load authentication configuration", "error", err)
		os.Exit(1)
	}
	if skipPasswordAuth {
		logger.Warn("game password validation is disabled; Steam64-only read sessions are enabled")
	}
	gameAPIKey := strings.TrimSpace(os.Getenv("STAR_GAME_API_KEY"))
	if gameAPIKey == "" {
		logger.Warn("STAR_GAME_API_KEY is empty; the game-server password update endpoint and websocket control plane are disabled")
	}
	gameWS := gamews.NewHub(logger, gameAPIKey)
	if gameWS.Enabled() {
		logger.Info("game websocket control plane enabled", "path", "/internal/v1/ws/game")
	}
	var equipment api.EquipmentService
	clientPrefsURL := strings.TrimSpace(os.Getenv("STAR_CLIENT_PREFS_API_URL"))
	if clientPrefsURL == "" {
		logger.Warn("STAR_CLIENT_PREFS_API_URL is empty; equipment read and mutation endpoints are disabled")
	} else {
		client, err := clientprefs.New(
			clientPrefsURL,
			os.Getenv("STAR_CLIENT_PREFS_API_KEY"),
			envOrDefault("STAR_CLIENT_PREFS_API_KEY_HEADER", "X-Star-Api-Key"),
		)
		if err != nil {
			logger.Error("configure client preferences service", "error", err)
			os.Exit(1)
		}
		equipment = client
		logger.Info("client preferences equipment service configured")
	}

	var players api.PlayerRepository
	dsn, databaseConfigured, err := config.DatabaseDSN()
	if err != nil {
		logger.Error("load database configuration", "error", err)
		os.Exit(1)
	}
	challengeDSN, challengeConfigured, err := config.ChallengeDatabaseDSN()
	if err != nil {
		logger.Error("load challenge database configuration", "error", err)
		os.Exit(1)
	}
	if !databaseConfigured {
		logger.Warn("database is not configured; real login and inventory are unavailable")
	} else {
		repository, err := mysqlrepo.Open(context.Background(), dsn)
		if err != nil {
			logger.Error("connect inventory database", "error", err)
			os.Exit(1)
		}
		repository.SetGroupMembershipChecker(steamgroup.New(logger, 15*time.Minute))
		if challengeConfigured {
			if err := repository.ConnectChallenge(context.Background(), challengeDSN); err != nil {
				logger.Error("connect challenge database", "error", err)
				os.Exit(1)
			}
			logger.Info("challenge stardust database connected")
			if !repository.ChallengeCatalogAvailable() {
				logger.Warn("stardust catalog table is missing; apply migration and import StarDustStore.json before stardust items can be displayed")
			}
		}
		defer repository.Close()
		players = repository
		logger.Info("real inventory database connected")
	}

	server := &http.Server{
		Addr: addr,
		Handler: api.NewHandler(
			demo.NewStore(),
			players,
			logger,
			origins,
			skipPasswordAuth,
			api.WithGameAPIKey(gameAPIKey),
			api.WithGameWS(gameWS),
			api.WithEquipmentService(equipment),
		),
		// ReadHeaderTimeout still bounds request headers. Full Read/Write timeouts are
		// disabled so long-lived game WebSocket sessions are not killed; gamews applies
		// its own read-idle and write deadlines.
		ReadHeaderTimeout: 5 * time.Second,
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
