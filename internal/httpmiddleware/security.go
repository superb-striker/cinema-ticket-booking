package httpmiddleware

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/superb-striker/cinema/internal/utils"
)

type SecurityConfig struct {
	RequireHTTPS      bool
	AllowedOrigins    []string
	TrustedProxyCIDRs string
	HSTSMaxAge        time.Duration
}

func Security(config SecurityConfig, next http.Handler) (http.Handler, error) {
	trusted, err := parseCIDRs(config.TrustedProxyCIDRs)
	if err != nil {
		return nil, err
	}
	origins := make(map[string]struct{}, len(config.AllowedOrigins))
	for _, origin := range config.AllowedOrigins {
		origins[strings.TrimSuffix(origin, "/")] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secure := r.TLS != nil || forwardedHTTPS(r, trusted)
		if config.RequireHTTPS && !secure && !strings.HasPrefix(r.URL.Path, "/health/") {
			utils.WriteError(w, http.StatusUpgradeRequired, "https is required")
			return
		}
		origin := strings.TrimSuffix(r.Header.Get("Origin"), "/")
		if origin != "" {
			if _, ok := origins[origin]; !ok {
				utils.WriteError(w, http.StatusForbidden, "origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, X-Scanner-Key, X-Request-ID")
			w.Header().Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(self), geolocation=(), microphone=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://checkout.razorpay.com; frame-src https://api.razorpay.com https://*.razorpay.com; connect-src 'self' https://api.razorpay.com; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; form-action 'self'")
		if secure && config.HSTSMaxAge > 0 {
			w.Header().Set("Strict-Transport-Security", "max-age="+strconv.FormatInt(int64(config.HSTSMaxAge/time.Second), 10)+"; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	}), nil
}

func parseCIDRs(value string) ([]*net.IPNet, error) {
	var result []*net.IPNet
	for _, raw := range strings.Split(value, ",") {
		if raw = strings.TrimSpace(raw); raw != "" {
			_, network, err := net.ParseCIDR(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
			}
			result = append(result, network)
		}
	}
	return result, nil
}

func forwardedHTTPS(r *http.Request, trusted []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	for _, network := range trusted {
		if peer != nil && network.Contains(peer) {
			return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
		}
	}
	return false
}
