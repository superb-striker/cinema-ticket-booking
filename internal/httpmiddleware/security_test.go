package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecurityCORSAndHeaders(t *testing.T) {
	handler, err := Security(SecurityConfig{AllowedOrigins: []string{"https://app.example"}, HSTSMaxAge: time.Hour},
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://api.example/test", nil)
	req.Header.Set("Origin", "https://app.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Access-Control-Allow-Origin") != "https://app.example" {
		t.Fatalf("unexpected allowed response: %d %#v", recorder.Code, recorder.Header())
	}
	if recorder.Header().Get("Strict-Transport-Security") == "" || recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("expected HSTS and CSP headers")
	}
}

func TestSecurityRejectsOriginAndPlainHTTP(t *testing.T) {
	handler, err := Security(SecurityConfig{RequireHTTPS: true, AllowedOrigins: []string{"https://app.example"}}, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://api.example/test", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUpgradeRequired {
		t.Fatalf("expected 426, got %d", recorder.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "https://api.example/test", nil)
	req.Header.Set("Origin", "https://evil.example")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", recorder.Code)
	}
}
