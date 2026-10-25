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

	"github.com/superb-striker/cinema/internal/adapters/redis"
	"github.com/superb-striker/cinema/internal/admin"
	"github.com/superb-striker/cinema/internal/auth"
	"github.com/superb-striker/cinema/internal/checkout"
	"github.com/superb-striker/cinema/internal/config"
	"github.com/superb-striker/cinema/internal/database"
	"github.com/superb-striker/cinema/internal/httpmiddleware"
	"github.com/superb-striker/cinema/internal/notification"
	"github.com/superb-striker/cinema/internal/observability"
	"github.com/superb-striker/cinema/internal/utils"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		slog.Error("parse log level", "error", err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownTracing, err := observability.ConfigureTracing(ctx, observability.TracingConfig{Enabled: cfg.TracingEnabled,
		ServiceName: cfg.TracingServiceName, Environment: cfg.Environment, SampleRatio: cfg.TracingSampleRatio})
	if err != nil {
		logger.Error("configure tracing", "error", err)
		os.Exit(1)
	}
	defer func() {
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer flushCancel()
		if err := shutdownTracing(flushCtx); err != nil {
			logger.Error("flush tracing", "error", err)
		}
	}()
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	rdb, err := redis.Open(redis.Config{Addr: cfg.RedisAddr, Username: cfg.RedisUsername, Password: cfg.RedisPassword,
		DB: cfg.RedisDB, TLSEnabled: cfg.RedisTLSEnabled, TLSServerName: cfg.RedisTLSServerName,
		TLSCAFile: cfg.RedisTLSCAFile, TLSCertFile: cfg.RedisTLSCertFile, TLSKeyFile: cfg.RedisTLSKeyFile,
		PoolSize: cfg.RedisPoolSize, MinIdleConns: cfg.RedisMinIdleConns})
	if err != nil {
		logger.Error("open redis", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()

	authService := auth.NewService(db, cfg.SessionTTL, cfg.CookieSecure, cfg.PublicBaseURL, cfg.VerificationSecret)
	if err := authService.ConfigureGoogle(ctx, cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL); err != nil {
		logger.Error("configure Google login", "error", err)
		os.Exit(1)
	}
	if err := authService.ConfigureAdminRoles(ctx, cfg.AdminEmails); err != nil {
		logger.Error("configure administrator roles", "error", err)
		os.Exit(1)
	}
	authHandler := auth.NewHandler(authService)
	adminCatalog := admin.NewCatalogHandler(db)
	go authService.RunMaintenance(ctx, time.Hour)
	repo := checkout.NewPostgresRepository(db)
	holds := checkout.NewRedisHoldStore(rdb)
	payments := checkout.NewRazorpay(cfg.RazorpayKeyID, cfg.RazorpayKeySecret, cfg.RazorpayWebhookSecret)
	checkoutService := checkout.NewService(repo, holds, payments, cfg.HoldTTL)
	checkoutHandler := checkout.NewHandler(checkoutService, repo, payments, cfg.TicketSigningSecret, cfg.ScannerAPIKey)
	worker := checkout.NewWorker(repo, payments, cfg.WorkerInterval)
	go worker.Run(ctx)
	notificationWorker := notification.NewWorker(db, notification.Config{PublicBaseURL: cfg.PublicBaseURL,
		LogOnly: cfg.NotificationLogOnly, SMTPAddr: cfg.SMTPAddr, SMTPUsername: cfg.SMTPUsername,
		SMTPPassword: cfg.SMTPPassword, SMTPFrom: cfg.SMTPFrom, SMSWebhookURL: cfg.SMSWebhookURL,
		SMSAPIToken: cfg.SMSAPIToken}, cfg.WorkerInterval)
	go notificationWorker.Run(ctx)
	limiter, err := httpmiddleware.NewRateLimiter(rdb, "cinema", cfg.TrustedProxyCIDRs)
	if err != nil {
		logger.Error("configure rate limiter", "error", err)
		os.Exit(1)
	}
	metrics := observability.NewMetrics(db, rdb)

	mux := http.NewServeMux()
	userKey := func(r *http.Request) string { user, _ := auth.UserFromContext(r.Context()); return user.ID.String() }
	mux.Handle("POST /api/v1/auth/register", limiter.Limit("register", 5, time.Hour, http.HandlerFunc(authHandler.Register)))
	mux.Handle("POST /api/v1/auth/login", limiter.Limit("login", 10, 5*time.Minute, http.HandlerFunc(authHandler.Login)))
	mux.Handle("POST /api/v1/auth/password-reset/request", limiter.Limit("password-reset", 5, time.Hour, http.HandlerFunc(authHandler.RequestPasswordReset)))
	mux.Handle("POST /api/v1/auth/password-reset/confirm", limiter.Limit("password-reset-confirm", 10, time.Hour, http.HandlerFunc(authHandler.ResetPassword)))
	mux.Handle("POST /api/v1/auth/email-verification/confirm", limiter.Limit("email-verification-confirm", 20, time.Hour, http.HandlerFunc(authHandler.ConfirmEmail)))
	mux.HandleFunc("GET /api/v1/auth/google", authHandler.GoogleLogin)
	mux.HandleFunc("GET /api/v1/auth/google/callback", authHandler.GoogleCallback)
	mux.HandleFunc("GET /api/v1/client-config", func(w http.ResponseWriter, _ *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]bool{"google_login": cfg.GoogleClientID != ""})
	})
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)
	mux.Handle("GET /api/v1/auth/me", authService.Require(http.HandlerFunc(authHandler.Me)))
	mux.Handle("PATCH /api/v1/auth/profile", authService.Require(http.HandlerFunc(authHandler.UpdateProfile)))
	mux.Handle("POST /api/v1/auth/email-verification/request", authService.Require(limiter.LimitBy("email-verification", 3, time.Hour, userKey, http.HandlerFunc(authHandler.RequestEmailVerification))))
	mux.Handle("POST /api/v1/auth/phone-verification/request", authService.Require(limiter.LimitBy("phone-verification", 3, time.Hour, userKey, http.HandlerFunc(authHandler.RequestPhoneVerification))))
	mux.Handle("POST /api/v1/auth/phone-verification/confirm", authService.Require(limiter.LimitBy("phone-verification-confirm", 10, time.Hour, userKey, http.HandlerFunc(authHandler.ConfirmPhone))))
	mux.Handle("GET /api/v1/admin/users", authService.RequireRole(http.HandlerFunc(authHandler.AdminUsers), "admin"))
	mux.Handle("PUT /api/v1/admin/users/{userID}/roles/{role}", authService.RequireRole(http.HandlerFunc(authHandler.GrantRole), "admin"))
	mux.Handle("DELETE /api/v1/admin/users/{userID}/roles/{role}", authService.RequireRole(http.HandlerFunc(authHandler.RevokeRole), "admin"))
	adminOnly := func(handler http.HandlerFunc) http.Handler {
		return authService.RequireRole(handler, "admin")
	}
	mux.Handle("GET /api/v1/admin/movies", adminOnly(adminCatalog.Movies))
	mux.Handle("POST /api/v1/admin/movies", adminOnly(adminCatalog.Movies))
	mux.Handle("PUT /api/v1/admin/movies/{movieID}", adminOnly(adminCatalog.Movie))
	mux.Handle("DELETE /api/v1/admin/movies/{movieID}", adminOnly(adminCatalog.Movie))
	mux.Handle("GET /api/v1/admin/cinemas", adminOnly(adminCatalog.Cinemas))
	mux.Handle("POST /api/v1/admin/cinemas", adminOnly(adminCatalog.Cinemas))
	mux.Handle("PUT /api/v1/admin/cinemas/{cinemaID}", adminOnly(adminCatalog.Cinema))
	mux.Handle("DELETE /api/v1/admin/cinemas/{cinemaID}", adminOnly(adminCatalog.Cinema))
	mux.Handle("GET /api/v1/admin/auditoriums", adminOnly(adminCatalog.Auditoriums))
	mux.Handle("POST /api/v1/admin/auditoriums", adminOnly(adminCatalog.Auditoriums))
	mux.Handle("PUT /api/v1/admin/auditoriums/{auditoriumID}", adminOnly(adminCatalog.Auditorium))
	mux.Handle("DELETE /api/v1/admin/auditoriums/{auditoriumID}", adminOnly(adminCatalog.Auditorium))
	mux.Handle("GET /api/v1/admin/auditoriums/{auditoriumID}/layout", adminOnly(adminCatalog.Layout))
	mux.Handle("PUT /api/v1/admin/auditoriums/{auditoriumID}/layout", adminOnly(adminCatalog.Layout))
	mux.Handle("GET /api/v1/admin/pricing-tiers", adminOnly(adminCatalog.PricingTiers))
	mux.Handle("POST /api/v1/admin/pricing-tiers", adminOnly(adminCatalog.PricingTiers))
	mux.Handle("PUT /api/v1/admin/pricing-tiers/{tierID}", adminOnly(adminCatalog.PricingTier))
	mux.Handle("DELETE /api/v1/admin/pricing-tiers/{tierID}", adminOnly(adminCatalog.PricingTier))
	mux.Handle("GET /api/v1/admin/shows", adminOnly(adminCatalog.Shows))
	mux.Handle("POST /api/v1/admin/shows", adminOnly(adminCatalog.Shows))
	mux.Handle("PUT /api/v1/admin/shows/{showID}", adminOnly(adminCatalog.Show))
	mux.Handle("DELETE /api/v1/admin/shows/{showID}", adminOnly(adminCatalog.Show))
	mux.Handle("GET /api/v1/admin/seat-blocks", adminOnly(adminCatalog.SeatBlocks))
	mux.Handle("POST /api/v1/admin/seat-blocks", adminOnly(adminCatalog.SeatBlocks))
	mux.Handle("DELETE /api/v1/admin/seat-blocks/{blockID}", adminOnly(adminCatalog.ReleaseSeatBlock))
	mux.Handle("GET /api/v1/admin/promotions", adminOnly(adminCatalog.Promotions))
	mux.Handle("POST /api/v1/admin/promotions", adminOnly(adminCatalog.Promotions))
	mux.Handle("PUT /api/v1/admin/promotions/{promotionID}", adminOnly(adminCatalog.Promotion))
	mux.Handle("DELETE /api/v1/admin/promotions/{promotionID}", adminOnly(adminCatalog.Promotion))
	supportOrAdmin := func(handler http.HandlerFunc) http.Handler {
		return authService.RequireRole(handler, "support", "admin")
	}
	mux.Handle("GET /api/v1/admin/bookings", supportOrAdmin(adminCatalog.SupportBookings))
	mux.Handle("GET /api/v1/admin/bookings/{orderID}", supportOrAdmin(adminCatalog.SupportBooking))
	mux.Handle("POST /api/v1/admin/bookings/{orderID}/cancel", supportOrAdmin(adminCatalog.SupportCancel))
	mux.Handle("POST /api/v1/admin/bookings/{orderID}/refund", supportOrAdmin(adminCatalog.SupportRefund))
	mux.Handle("GET /api/v1/admin/bookings/{orderID}/notes", supportOrAdmin(adminCatalog.SupportNotes))
	mux.Handle("POST /api/v1/admin/bookings/{orderID}/notes", supportOrAdmin(adminCatalog.SupportNotes))
	mux.Handle("GET /api/v1/admin/reports/sales", adminOnly(adminCatalog.SalesReport))
	mux.Handle("GET /api/v1/admin/reports/occupancy", adminOnly(adminCatalog.OccupancyReport))
	mux.Handle("GET /api/v1/admin/reports/payment-failures", adminOnly(adminCatalog.PaymentFailuresReport))
	mux.Handle("GET /api/v1/admin/reports/revenue", adminOnly(adminCatalog.RevenueReport))
	mux.HandleFunc("GET /api/v1/screenings", checkoutHandler.ListScreenings)
	mux.HandleFunc("GET /api/v1/screenings/{screeningID}/seats", checkoutHandler.Seats)
	mux.HandleFunc("GET /api/v1/screenings/{screeningID}/seat-events", checkoutHandler.SeatEvents)
	mux.Handle("POST /api/v1/screenings/{screeningID}/holds", authService.Require(limiter.LimitBy("holds", 30, time.Minute, userKey, http.HandlerFunc(checkoutHandler.CreateHold))))
	mux.Handle("POST /api/v1/screenings/{screeningID}/holds/{holdID}/checkout", authService.Require(limiter.LimitBy("checkout", 10, time.Minute, userKey, http.HandlerFunc(checkoutHandler.Checkout))))
	mux.Handle("GET /api/v1/orders/{orderID}", authService.Require(http.HandlerFunc(checkoutHandler.Order)))
	mux.Handle("GET /api/v1/bookings", authService.Require(http.HandlerFunc(checkoutHandler.Bookings)))
	mux.Handle("POST /api/v1/bookings/{orderID}/cancel", authService.Require(limiter.LimitBy("cancel", 5, time.Hour, userKey, http.HandlerFunc(checkoutHandler.CancelBooking))))
	mux.Handle("GET /api/v1/bookings/{orderID}/ticket", authService.Require(http.HandlerFunc(checkoutHandler.DownloadTicket)))
	mux.Handle("POST /api/v1/tickets/validate", limiter.Limit("ticket-scan", 120, time.Minute, http.HandlerFunc(checkoutHandler.ValidateTicket)))
	mux.Handle("POST /api/v1/orders/{orderID}/verify-payment", authService.Require(limiter.LimitBy("verify-payment", 20, time.Minute, userKey, http.HandlerFunc(checkoutHandler.VerifyPayment))))
	mux.HandleFunc("POST /api/v1/webhooks/razorpay", checkoutHandler.Webhook)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		checkCtx, done := context.WithTimeout(r.Context(), 2*time.Second)
		defer done()
		if err := db.Ping(checkCtx); err != nil || rdb.Ping(checkCtx).Err() != nil {
			utils.WriteError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	if cfg.MetricsEnabled {
		mux.Handle("GET /metrics", metrics.Handler(cfg.MetricsToken))
	}
	mux.Handle("/api", httpmiddleware.UnsupportedAPIVersion("v1"))
	mux.Handle("/api/", httpmiddleware.UnsupportedAPIVersion("v1"))
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/api-docs.html", http.StatusMovedPermanently)
	})
	staticFiles := http.FileServer(http.Dir("static"))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		staticFiles.ServeHTTP(w, r)
	}))

	versioned := httpmiddleware.APIVersion("v1", mux)
	secured, err := httpmiddleware.Security(httpmiddleware.SecurityConfig{RequireHTTPS: cfg.RequireHTTPS,
		AllowedOrigins: cfg.CORSAllowedOrigins, TrustedProxyCIDRs: cfg.TrustedProxyCIDRs, HSTSMaxAge: cfg.HSTSMaxAge}, versioned)
	if err != nil {
		logger.Error("configure HTTP security", "error", err)
		os.Exit(1)
	}
	observed := httpmiddleware.RequestObservability(logger, metrics, secured)
	traced := observability.TraceHTTP(observed)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: traced, ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		ReadTimeout: cfg.HTTPReadTimeout, WriteTimeout: cfg.HTTPWriteTimeout, IdleTimeout: cfg.HTTPIdleTimeout, MaxHeaderBytes: 1 << 20}
	go func() {
		logger.Info("cinema API listening", "address", cfg.HTTPAddr, "environment", cfg.Environment)
		var serveErr error
		if cfg.TLSCertFile != "" {
			serveErr = server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			serveErr = server.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", serveErr)
			cancel()
		}
	}()
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
	logger.Info("cinema API stopped")
}
