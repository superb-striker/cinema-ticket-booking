package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRateLimiterRejectsBurstByClientIP(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	limiter, err := NewRateLimiter(rdb, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	handler := limiter.Limit("login", 2, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for i, want := range []int{204, 204, 429} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.RemoteAddr = "203.0.113.8:4321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d: want %d got %d", i+1, want, rec.Code)
		}
	}
}

func TestRateLimiterTrustsForwardedForOnlyFromConfiguredProxy(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	limiter, err := NewRateLimiter(rdb, "test", "10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	trusted := httptest.NewRequest(http.MethodGet, "/", nil)
	trusted.RemoteAddr = "10.1.2.3:80"
	trusted.Header.Set("X-Forwarded-For", "203.0.113.4")
	untrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	untrusted.RemoteAddr = "192.0.2.9:80"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.5")
	if got := limiter.clientIP(trusted); got != "203.0.113.4" {
		t.Fatalf("trusted proxy IP=%q", got)
	}
	if got := limiter.clientIP(untrusted); got != "192.0.2.9" {
		t.Fatalf("untrusted proxy IP=%q", got)
	}
}
