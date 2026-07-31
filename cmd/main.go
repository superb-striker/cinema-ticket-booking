package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/superb-striker/cinema/internal/adapters/redis"
	"github.com/superb-striker/cinema/internal/auth"
	"github.com/superb-striker/cinema/internal/checkout"
	"github.com/superb-striker/cinema/internal/config"
	"github.com/superb-striker/cinema/internal/database"
	"github.com/superb-striker/cinema/internal/httpmiddleware"
	"github.com/superb-striker/cinema/internal/notification"
	"github.com/superb-striker/cinema/internal/utils"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	rdb, err := redis.NewClientWithPassword(cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		log.Fatal(err)
	}
	defer rdb.Close()

	authService := auth.NewService(db, cfg.SessionTTL, cfg.CookieSecure, cfg.PublicBaseURL, cfg.VerificationSecret)
	if err := authService.ConfigureGoogle(ctx, cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL); err != nil {
		log.Fatal(err)
	}
	authHandler := auth.NewHandler(authService)
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
		log.Fatal(err)
	}

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
	mux.Handle("GET /", http.FileServer(http.Dir("static")))

	server := &http.Server{Addr: cfg.HTTPAddr, Handler: securityHeaders(mux), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		log.Printf("cinema API listening on %s", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}()
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://checkout.razorpay.com; frame-src https://api.razorpay.com https://*.razorpay.com; connect-src 'self' https://api.razorpay.com; style-src 'self' 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}
