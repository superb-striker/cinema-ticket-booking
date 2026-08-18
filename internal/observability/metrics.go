package observability

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/superb-striker/cinema/internal/utils"
)

var durationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type metricKey struct {
	method, route, status string
}

type histogram struct {
	count   uint64
	sum     float64
	buckets [len(durationBuckets)]uint64
}

type Metrics struct {
	started   time.Time
	inFlight  atomic.Int64
	panics    atomic.Uint64
	mu        sync.RWMutex
	requests  map[metricKey]uint64
	durations map[metricKey]*histogram
	db        *pgxpool.Pool
	redis     *redis.Client
}

func NewMetrics(db *pgxpool.Pool, redisClient *redis.Client) *Metrics {
	return &Metrics{started: time.Now(), requests: make(map[metricKey]uint64),
		durations: make(map[metricKey]*histogram), db: db, redis: redisClient}
}

func (m *Metrics) BeginRequest() { m.inFlight.Add(1) }

func (m *Metrics) EndRequest(method, route string, status int, duration time.Duration) {
	m.inFlight.Add(-1)
	if route == "" {
		route = "unmatched"
	}
	key := metricKey{method: method, route: route, status: strconv.Itoa(status)}
	m.mu.Lock()
	m.requests[key]++
	h := m.durations[key]
	if h == nil {
		h = &histogram{}
		m.durations[key] = h
	}
	h.count++
	seconds := duration.Seconds()
	h.sum += seconds
	for i, bucket := range durationBuckets {
		if seconds <= bucket {
			h.buckets[i]++
		}
	}
	m.mu.Unlock()
}

func (m *Metrics) RecordPanic() { m.panics.Add(1) }

func (m *Metrics) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				utils.WriteError(w, http.StatusUnauthorized, "metrics authentication required")
				return
			}
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		m.writePrometheus(w)
	})
}

func (m *Metrics) writePrometheus(w http.ResponseWriter) {
	_, _ = fmt.Fprintf(w, "# HELP cinema_process_uptime_seconds Process uptime.\n# TYPE cinema_process_uptime_seconds gauge\ncinema_process_uptime_seconds %.3f\n", time.Since(m.started).Seconds())
	_, _ = fmt.Fprintf(w, "# HELP cinema_http_requests_in_flight Current HTTP requests.\n# TYPE cinema_http_requests_in_flight gauge\ncinema_http_requests_in_flight %d\n", m.inFlight.Load())
	_, _ = fmt.Fprintf(w, "# HELP cinema_http_panics_total Recovered HTTP panics.\n# TYPE cinema_http_panics_total counter\ncinema_http_panics_total %d\n", m.panics.Load())

	m.mu.RLock()
	keys := make([]metricKey, 0, len(m.requests))
	for key := range m.requests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].method+keys[i].route+keys[i].status < keys[j].method+keys[j].route+keys[j].status
	})
	_, _ = fmt.Fprintln(w, "# HELP cinema_http_requests_total HTTP requests by method, route, and status.")
	_, _ = fmt.Fprintln(w, "# TYPE cinema_http_requests_total counter")
	for _, key := range keys {
		labels := labels(key)
		_, _ = fmt.Fprintf(w, "cinema_http_requests_total{%s} %d\n", labels, m.requests[key])
	}
	_, _ = fmt.Fprintln(w, "# HELP cinema_http_request_duration_seconds HTTP request latency.")
	_, _ = fmt.Fprintln(w, "# TYPE cinema_http_request_duration_seconds histogram")
	for _, key := range keys {
		h := m.durations[key]
		base := labels(key)
		for i, bucket := range durationBuckets {
			_, _ = fmt.Fprintf(w, "cinema_http_request_duration_seconds_bucket{%s,le=%q} %d\n", base, strconv.FormatFloat(bucket, 'g', -1, 64), h.buckets[i])
		}
		_, _ = fmt.Fprintf(w, "cinema_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", base, h.count)
		_, _ = fmt.Fprintf(w, "cinema_http_request_duration_seconds_sum{%s} %.6f\n", base, h.sum)
		_, _ = fmt.Fprintf(w, "cinema_http_request_duration_seconds_count{%s} %d\n", base, h.count)
	}
	m.mu.RUnlock()

	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	_, _ = fmt.Fprintf(w, "# HELP cinema_go_goroutines Current goroutines.\n# TYPE cinema_go_goroutines gauge\ncinema_go_goroutines %d\n", runtime.NumGoroutine())
	_, _ = fmt.Fprintf(w, "# HELP cinema_go_heap_bytes Allocated heap bytes.\n# TYPE cinema_go_heap_bytes gauge\ncinema_go_heap_bytes %d\n", memory.HeapAlloc)
	if m.db != nil {
		stats := m.db.Stat()
		_, _ = fmt.Fprintf(w, "# HELP cinema_postgres_connections PostgreSQL pool connections.\n# TYPE cinema_postgres_connections gauge\ncinema_postgres_connections{state=\"acquired\"} %d\ncinema_postgres_connections{state=\"idle\"} %d\ncinema_postgres_connections{state=\"total\"} %d\n", stats.AcquiredConns(), stats.IdleConns(), stats.TotalConns())
	}
	if m.redis != nil {
		stats := m.redis.PoolStats()
		_, _ = fmt.Fprintf(w, "# HELP cinema_redis_connections Redis pool connections.\n# TYPE cinema_redis_connections gauge\ncinema_redis_connections{state=\"idle\"} %d\ncinema_redis_connections{state=\"total\"} %d\n", stats.IdleConns, stats.TotalConns)
		_, _ = fmt.Fprintf(w, "# HELP cinema_redis_pool_timeouts_total Redis pool wait timeouts.\n# TYPE cinema_redis_pool_timeouts_total counter\ncinema_redis_pool_timeouts_total %d\n", stats.Timeouts)
	}
}

func labels(key metricKey) string {
	return fmt.Sprintf("method=%q,route=%q,status=%q", escapeLabel(key.method), escapeLabel(key.route), escapeLabel(key.status))
}

func escapeLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
