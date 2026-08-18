package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsPrometheusOutputAndAuthentication(t *testing.T) {
	metrics := NewMetrics(nil, nil)
	metrics.BeginRequest()
	metrics.EndRequest(http.MethodGet, "GET /api/v1/screenings", http.StatusOK, 20*time.Millisecond)
	handler := metrics.Handler("a-long-metrics-access-token")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer a-long-metrics-access-token")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(),
		`cinema_http_requests_total{method="GET",route="GET /api/v1/screenings",status="200"} 1`) {
		t.Fatalf("unexpected metrics response: %d %s", recorder.Code, recorder.Body.String())
	}
}
