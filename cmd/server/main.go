package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/convoy/backend/internal/admin"
	"github.com/convoy/backend/internal/auth"
	"github.com/convoy/backend/internal/auth/providers"
	"github.com/convoy/backend/internal/config"
	"github.com/convoy/backend/internal/db"
	"github.com/convoy/backend/internal/feedback"
	"github.com/convoy/backend/internal/httpx"
	lk "github.com/convoy/backend/internal/livekit"
	"github.com/convoy/backend/internal/policy"
	"github.com/convoy/backend/internal/push"
	"github.com/convoy/backend/internal/realtime"
	"github.com/convoy/backend/internal/rooms"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		slog.Error("db migrate", "err", err)
		os.Exit(1)
	}

	authSvc := auth.NewService(pool, cfg.JWTSecret, cfg.JWTTTL)
	// OAuth verifiers are optional: hosts that haven't configured a
	// client id for a given provider just keep guest-only auth working.
	if len(cfg.GoogleClientIDs) > 0 {
		if v, err := providers.NewGoogleVerifier(ctx, cfg.GoogleClientIDs); err != nil {
			slog.Error("google verifier", "err", err)
		} else {
			authSvc.WithGoogleVerifier(v)
			slog.Info("google sign-in enabled", "audiences", len(cfg.GoogleClientIDs))
		}
	}
	if len(cfg.AppleClientIDs) > 0 {
		if v, err := providers.NewAppleVerifier(ctx, cfg.AppleClientIDs); err != nil {
			slog.Error("apple verifier", "err", err)
		} else {
			authSvc.WithAppleVerifier(v)
			slog.Info("apple sign-in enabled", "audiences", len(cfg.AppleClientIDs))
		}
	}

	appConfigStore := config.NewAppConfigStore(pool)
	roomStore := rooms.NewStore(pool)
	hub := realtime.NewHub(roomStore)
	roomSvc := rooms.NewService(roomStore, hub)
	livekitCfg := lk.Config{
		URL:       cfg.LiveKitURL,
		APIKey:    cfg.LiveKitAPIKey,
		APISecret: cfg.LiveKitAPISecret,
	}
	roomHandlers := rooms.NewHandlers(roomSvc, livekitCfg)
	wsHandler := realtime.NewHandler(hub, authSvc, roomStore, cfg.WSPingInterval)

	feedbackStore := feedback.NewStore(pool)
	feedbackHandlers := feedback.NewHandlers(feedbackStore)

	pushStore := push.NewStore(pool)
	pushHandlers := push.NewHandlers(pushStore)
	expoClient := push.NewExpoClient(cfg.ExpoAccessToken)

	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(requestLogger)
	r.Use(corsMiddleware(cfg.AllowedOrigins))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Root hits the dashboard. The admin module's own middleware decides
	// whether to land on /admin/ or /admin/login.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})

	// Public runtime config (min-client-version etc). Unauthenticated by
	// design — the mobile app needs to read it BEFORE sign-in to decide
	// whether to render the UpdateRequired screen.
	r.Get("/config", appConfigStore.Handler)

	// Public privacy policy. Mounted conditionally because the page can't
	// render without a contact email; admins running locally without one
	// just don't get the route (linked from settings, which is hidden when
	// the URL 404s on the client anyway).
	if cfg.PrivacyContactEmail != "" {
		if pol, err := policy.New(policy.Config{
			AppName:       cfg.AppName,
			ContactEmail:  cfg.PrivacyContactEmail,
			EffectiveDate: cfg.PrivacyEffectiveDate,
		}); err != nil {
			slog.Error("policy disabled", "err", err)
		} else {
			r.Get("/policy", pol.Handler)
			slog.Info("policy page enabled")
		}
	} else {
		slog.Info("policy disabled", "reason", "PRIVACY_CONTACT_EMAIL not set")
	}

	r.Post("/auth/guest", authSvc.HandleGuest)
	r.Post("/auth/google", authSvc.HandleGoogle)
	r.Post("/auth/apple", authSvc.HandleApple)

	r.Group(func(r chi.Router) {
		r.Use(authSvc.Middleware)
		r.Get("/me", authSvc.HandleMe)
		r.Patch("/me", authSvc.HandlePatchMe)
		r.Delete("/account", authSvc.HandleDeleteAccount)
		r.Route("/rooms", roomHandlers.Routes)
		r.Post("/feedback", feedbackHandlers.Submit)
		r.Post("/push-tokens", pushHandlers.Save)
	})

	if adminMod, err := admin.New(admin.Config{
		Password:     cfg.AdminPassword,
		CookieSecret: cfg.AdminCookieSecret,
		Pool:         pool,
		Feedback:     feedbackStore,
		Push:         pushStore,
		Expo:         expoClient,
		AppCfg:       appConfigStore,
	}); err != nil {
		slog.Info("admin disabled", "reason", err)
	} else {
		adminMod.Mount(r)
	}

	r.Handle("/ws", wsHandler)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"dur_ms", time.Since(start).Milliseconds(),
		)
	})
}

func corsMiddleware(allowed []string) func(http.Handler) http.Handler {
	allowAll := len(allowed) == 1 && allowed[0] == "*"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				for _, a := range allowed {
					if a == origin {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						break
					}
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "300")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
