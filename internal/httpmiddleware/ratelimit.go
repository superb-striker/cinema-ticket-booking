package httpmiddleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/superb-striker/cinema/internal/utils"
)

type RateLimiter struct {
	rdb            *redis.Client
	namespace      string
	trustedProxies []*net.IPNet
}

func NewRateLimiter(rdb *redis.Client, namespace, trustedCIDRs string) (*RateLimiter, error) {
	limiter := &RateLimiter{rdb: rdb, namespace: namespace}
	for _, raw := range strings.Split(trustedCIDRs, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", raw, err)
		}
		limiter.trustedProxies = append(limiter.trustedProxies, network)
	}
	return limiter, nil
}

var tokenBucketScript = redis.NewScript(`
local t=redis.call('TIME')
local now=(tonumber(t[1])*1000)+math.floor(tonumber(t[2])/1000)
local values=redis.call('HMGET',KEYS[1],'tokens','updated')
local capacity=tonumber(ARGV[1])
local refill=tonumber(ARGV[2])
local tokens=tonumber(values[1]) or capacity
local updated=tonumber(values[2]) or now
tokens=math.min(capacity,tokens+math.max(0,now-updated)*refill/1000)
local allowed=0
local retry=0
if tokens>=1 then tokens=tokens-1 allowed=1 else retry=math.ceil((1-tokens)*1000/refill) end
redis.call('HSET',KEYS[1],'tokens',tokens,'updated',now)
redis.call('PEXPIRE',KEYS[1],math.ceil((capacity/refill)*2000))
return {allowed,retry}
`)

func (l *RateLimiter) Limit(scope string, capacity int, per time.Duration, next http.Handler) http.Handler {
	return l.LimitBy(scope, capacity, per, func(r *http.Request) string { return l.clientIP(r) }, next)
}

func (l *RateLimiter) LimitBy(scope string, capacity int, per time.Duration, identity func(*http.Request) string, next http.Handler) http.Handler {
	refill := float64(capacity) / per.Seconds()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256([]byte(identity(r)))
		key := fmt.Sprintf("%s:rate:%s:%s", l.namespace, scope, hex.EncodeToString(sum[:16]))
		ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
		defer cancel()
		result, err := tokenBucketScript.Run(ctx, l.rdb, []string{key}, capacity, refill).Int64Slice()
		if err != nil || len(result) != 2 {
			utils.WriteError(w, http.StatusServiceUnavailable, "rate limiter unavailable")
			return
		}
		if result[0] != 1 {
			seconds := (result[1] + 999) / 1000
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			utils.WriteError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *RateLimiter) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(host)
	for _, network := range l.trustedProxies {
		if peer != nil && network.Contains(peer) {
			parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
			if len(parts) > 0 {
				candidate := strings.TrimSpace(parts[0])
				if net.ParseIP(candidate) != nil {
					return candidate
				}
			}
		}
	}
	return host
}
