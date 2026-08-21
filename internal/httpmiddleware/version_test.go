package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIVersionAddsDiscoveryHeadersOnlyToAPI(t *testing.T) {
	handler := APIVersion("v1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/v1/screenings", nil))
	if apiResponse.Header().Get("API-Version") != "v1" || !strings.Contains(apiResponse.Header().Get("Link"), "openapi.yaml") {
		t.Fatalf("missing API discovery headers: %#v", apiResponse.Header())
	}

	pageResponse := httptest.NewRecorder()
	handler.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if pageResponse.Header().Get("API-Version") != "" {
		t.Fatalf("unexpected API version on web page: %#v", pageResponse.Header())
	}
}

func TestUnsupportedAPIVersionReturnsMachineReadableError(t *testing.T) {
	response := httptest.NewRecorder()
	UnsupportedAPIVersion("v1").ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/screenings", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "supported_versions") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}
