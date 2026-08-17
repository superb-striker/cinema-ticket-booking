package config

import (
	"strings"
	"testing"
)

func validTestEnv(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"APP_ENV": "test", "DATABASE_URL": "postgres://cinema:secret@localhost:5432/cinema",
		"RAZORPAY_KEY_ID": "rzp_test_valid", "RAZORPAY_KEY_SECRET": "test-secret",
		"RAZORPAY_WEBHOOK_SECRET": "webhook-secret", "PUBLIC_BASE_URL": "http://localhost:8080",
		"TICKET_SIGNING_SECRET": strings.Repeat("t", 32), "SCANNER_API_KEY": strings.Repeat("s", 24),
		"VERIFICATION_SECRET": strings.Repeat("v", 32), "NOTIFICATION_LOG_ONLY": "true",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func TestLoadRejectsMalformedRuntimeValues(t *testing.T) {
	validTestEnv(t)
	t.Setenv("HTTP_READ_TIMEOUT", "eventually")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "HTTP_READ_TIMEOUT") {
		t.Fatalf("expected duration validation error, got %v", err)
	}
	t.Setenv("HTTP_READ_TIMEOUT", "10s")
	t.Setenv("COOKIE_SECURE", "sometimes")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("expected boolean validation error, got %v", err)
	}
}

func TestLoadAddsPublicOriginToCORSPolicy(t *testing.T) {
	validTestEnv(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://ops.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CORSAllowedOrigins) != 2 || cfg.CORSAllowedOrigins[1] != "http://localhost:8080" {
		t.Fatalf("unexpected origins: %#v", cfg.CORSAllowedOrigins)
	}
}

func TestLoadRequiresSecureProductionTransport(t *testing.T) {
	validTestEnv(t)
	t.Setenv("APP_ENV", "production")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "production requires") {
		t.Fatalf("expected secure production validation error, got %v", err)
	}
}
