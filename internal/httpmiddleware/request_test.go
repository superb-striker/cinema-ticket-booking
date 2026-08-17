package httpmiddleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestObservabilityPreservesRequestIDAndLogs(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := RequestObservability(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestID(r.Context()) != "client-request-123" {
			t.Fatalf("request ID missing from context")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	req.Header.Set("X-Request-ID", "client-request-123")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Header().Get("X-Request-ID") != "client-request-123" || recorder.Code != http.StatusCreated {
		t.Fatalf("unexpected response: %d %#v", recorder.Code, recorder.Header())
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["request_id"] != "client-request-123" || entry["status"] != float64(http.StatusCreated) {
		t.Fatalf("unexpected log: %#v", entry)
	}
}

func TestRequestObservabilityRecoversPanic(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	handler := RequestObservability(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "internal server error") {
		t.Fatalf("unexpected panic response: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRequestIDContextMissing(t *testing.T) {
	if RequestID(context.Background()) != "" {
		t.Fatal("expected no request ID")
	}
}
