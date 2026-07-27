package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr              string
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
	c := Config{
		HTTPAddr:              env("HTTP_ADDR", ":8080"),
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
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if c.RazorpayKeyID == "" || c.RazorpayKeySecret == "" || c.RazorpayWebhookSecret == "" {
		return Config{}, fmt.Errorf("RAZORPAY_KEY_ID, RAZORPAY_KEY_SECRET, and RAZORPAY_WEBHOOK_SECRET are required")
	}
	if len(c.TicketSigningSecret) < 32 || len(c.ScannerAPIKey) < 24 {
		return Config{}, fmt.Errorf("TICKET_SIGNING_SECRET (32+ characters) and SCANNER_API_KEY (24+ characters) are required")
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
