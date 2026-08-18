package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment           string
	LogLevel              string
	MetricsEnabled        bool
	MetricsToken          string
	HTTPAddr              string
	HTTPReadHeaderTimeout time.Duration
	HTTPReadTimeout       time.Duration
	HTTPWriteTimeout      time.Duration
	HTTPIdleTimeout       time.Duration
	ShutdownTimeout       time.Duration
	TLSCertFile           string
	TLSKeyFile            string
	RequireHTTPS          bool
	CORSAllowedOrigins    []string
	HSTSMaxAge            time.Duration
	DatabaseURL           string
	RedisAddr             string
	RedisPassword         string
	CookieSecure          bool
	SessionTTL            time.Duration
	HoldTTL               time.Duration
	WorkerInterval        time.Duration
	TrustedProxyCIDRs     string
	RazorpayKeyID         string
	RazorpayKeySecret     string
	RazorpayWebhookSecret string
	PublicBaseURL         string
	TicketSigningSecret   string
	ScannerAPIKey         string
	VerificationSecret    string
	AdminEmails           string
	GoogleClientID        string
	GoogleClientSecret    string
	GoogleRedirectURL     string
	NotificationLogOnly   bool
	SMTPAddr              string
	SMTPUsername          string
	SMTPPassword          string
	SMTPFrom              string
	SMSWebhookURL         string
	SMSAPIToken           string
}

func Load() (Config, error) {
	if err := validateBoolEnvs("COOKIE_SECURE", "REQUIRE_HTTPS", "NOTIFICATION_LOG_ONLY", "METRICS_ENABLED"); err != nil {
		return Config{}, err
	}
	readHeaderTimeout, err := envDuration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := envDuration("HTTP_READ_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := envDuration("HTTP_WRITE_TIMEOUT", 20*time.Second)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := envDuration("HTTP_IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := envDuration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	hstsMaxAge, err := envDuration("HSTS_MAX_AGE", 365*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	c := Config{
		Environment:           strings.ToLower(env("APP_ENV", "development")),
		LogLevel:              strings.ToLower(env("LOG_LEVEL", "info")),
		MetricsEnabled:        envBool("METRICS_ENABLED", true),
		MetricsToken:          os.Getenv("METRICS_TOKEN"),
		HTTPAddr:              env("HTTP_ADDR", ":8080"),
		HTTPReadHeaderTimeout: readHeaderTimeout,
		HTTPReadTimeout:       readTimeout,
		HTTPWriteTimeout:      writeTimeout,
		HTTPIdleTimeout:       idleTimeout,
		ShutdownTimeout:       shutdownTimeout,
		TLSCertFile:           os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:            os.Getenv("TLS_KEY_FILE"),
		RequireHTTPS:          envBool("REQUIRE_HTTPS", false),
		CORSAllowedOrigins:    splitCSV(os.Getenv("CORS_ALLOWED_ORIGINS")),
		HSTSMaxAge:            hstsMaxAge,
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RedisAddr:             env("REDIS_ADDR", "localhost:6379"),
		RedisPassword:         os.Getenv("REDIS_PASSWORD"),
		CookieSecure:          envBool("COOKIE_SECURE", true),
		SessionTTL:            30 * 24 * time.Hour,
		HoldTTL:               10 * time.Minute,
		WorkerInterval:        10 * time.Second,
		TrustedProxyCIDRs:     os.Getenv("TRUSTED_PROXY_CIDRS"),
		RazorpayKeyID:         os.Getenv("RAZORPAY_KEY_ID"),
		RazorpayKeySecret:     os.Getenv("RAZORPAY_KEY_SECRET"),
		RazorpayWebhookSecret: os.Getenv("RAZORPAY_WEBHOOK_SECRET"),
		PublicBaseURL:         env("PUBLIC_BASE_URL", "http://localhost:8080"),
		TicketSigningSecret:   os.Getenv("TICKET_SIGNING_SECRET"),
		ScannerAPIKey:         os.Getenv("SCANNER_API_KEY"),
		VerificationSecret:    os.Getenv("VERIFICATION_SECRET"),
		AdminEmails:           os.Getenv("ADMIN_EMAILS"),
		GoogleClientID:        os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:    os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:     os.Getenv("GOOGLE_REDIRECT_URL"),
		NotificationLogOnly:   envBool("NOTIFICATION_LOG_ONLY", false),
		SMTPAddr:              os.Getenv("SMTP_ADDR"),
		SMTPUsername:          os.Getenv("SMTP_USERNAME"),
		SMTPPassword:          os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:              os.Getenv("SMTP_FROM"),
		SMSWebhookURL:         os.Getenv("SMS_WEBHOOK_URL"),
		SMSAPIToken:           os.Getenv("SMS_API_TOKEN"),
	}
	if c.Environment != "development" && c.Environment != "test" && c.Environment != "production" {
		return Config{}, fmt.Errorf("APP_ENV must be development, test, or production")
	}
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return Config{}, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR must contain a valid host and port: %w", err)
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if parsed, err := url.Parse(c.DatabaseURL); err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must be a valid postgres URL")
	}
	if c.RazorpayKeyID == "" || c.RazorpayKeySecret == "" || c.RazorpayWebhookSecret == "" {
		return Config{}, fmt.Errorf("RAZORPAY_KEY_ID, RAZORPAY_KEY_SECRET, and RAZORPAY_WEBHOOK_SECRET are required")
	}
	if len(c.TicketSigningSecret) < 32 || len(c.ScannerAPIKey) < 24 || len(c.VerificationSecret) < 32 {
		return Config{}, fmt.Errorf("TICKET_SIGNING_SECRET and VERIFICATION_SECRET (32+ characters) and SCANNER_API_KEY (24+ characters) are required")
	}
	googleValues := 0
	for _, value := range []string{c.GoogleClientID, c.GoogleClientSecret, c.GoogleRedirectURL} {
		if value != "" {
			googleValues++
		}
	}
	if googleValues != 0 && googleValues != 3 {
		return Config{}, fmt.Errorf("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and GOOGLE_REDIRECT_URL must be configured together")
	}
	if !c.NotificationLogOnly && c.SMTPAddr == "" {
		return Config{}, fmt.Errorf("SMTP delivery must be configured unless NOTIFICATION_LOG_ONLY=true")
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return Config{}, fmt.Errorf("TLS_CERT_FILE and TLS_KEY_FILE must be configured together")
	}
	publicURL, err := url.Parse(c.PublicBaseURL)
	if err != nil || publicURL.Host == "" || (publicURL.Scheme != "http" && publicURL.Scheme != "https") {
		return Config{}, fmt.Errorf("PUBLIC_BASE_URL must be an absolute http or https URL")
	}
	publicOrigin := publicURL.Scheme + "://" + publicURL.Host
	foundPublicOrigin := false
	for _, origin := range c.CORSAllowedOrigins {
		foundPublicOrigin = foundPublicOrigin || origin == publicOrigin
	}
	if !foundPublicOrigin {
		c.CORSAllowedOrigins = append(c.CORSAllowedOrigins, publicOrigin)
	}
	for _, origin := range c.CORSAllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Config{}, fmt.Errorf("invalid CORS_ALLOWED_ORIGINS value %q", origin)
		}
	}
	if c.HTTPReadHeaderTimeout <= 0 || c.HTTPReadTimeout <= 0 || c.HTTPWriteTimeout <= 0 ||
		c.HTTPIdleTimeout <= 0 || c.ShutdownTimeout <= 0 || c.HSTSMaxAge < 0 {
		return Config{}, fmt.Errorf("HTTP, shutdown, and HSTS durations must be positive")
	}
	if c.Environment == "production" {
		if publicURL.Scheme != "https" || !c.CookieSecure || !c.RequireHTTPS {
			return Config{}, fmt.Errorf("production requires an https PUBLIC_BASE_URL, COOKIE_SECURE=true, and REQUIRE_HTTPS=true")
		}
		if c.NotificationLogOnly {
			return Config{}, fmt.Errorf("NOTIFICATION_LOG_ONLY must be false in production")
		}
		if c.MetricsEnabled && len(c.MetricsToken) < 24 {
			return Config{}, fmt.Errorf("production METRICS_TOKEN must contain at least 24 characters when metrics are enabled")
		}
		if strings.HasPrefix(c.RazorpayKeyID, "rzp_test_") {
			return Config{}, fmt.Errorf("Razorpay test credentials are not allowed in production")
		}
		for _, value := range []string{c.RazorpayKeyID, c.RazorpayKeySecret, c.RazorpayWebhookSecret,
			c.TicketSigningSecret, c.ScannerAPIKey, c.VerificationSecret} {
			lower := strings.ToLower(value)
			if strings.Contains(lower, "replace") || strings.Contains(lower, "change-in-production") || strings.Contains(lower, "your_") {
				return Config{}, fmt.Errorf("placeholder secrets are not allowed in production")
			}
		}
	}
	return c, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}
	return parsed, nil
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, strings.TrimSuffix(item, "/"))
		}
	}
	return result
}

func validateBoolEnvs(keys ...string) error {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			if _, err := strconv.ParseBool(value); err != nil {
				return fmt.Errorf("%s must be true or false", key)
			}
		}
	}
	return nil
}
