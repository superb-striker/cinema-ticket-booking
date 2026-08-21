package httpmiddleware

import (
	"net/http"
	"strings"

	"github.com/superb-striker/cinema/internal/utils"
)

func APIVersion(version string, next http.Handler) http.Handler {
	prefix := "/api/" + version + "/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			w.Header().Set("API-Version", version)
			w.Header().Add("Link", `</openapi.yaml>; rel="service-desc"; type="application/vnd.oai.openapi"`)
		}
		next.ServeHTTP(w, r)
	})
}

func UnsupportedAPIVersion(current string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("API-Version", current)
		w.Header().Add("Link", `</openapi.yaml>; rel="service-desc"; type="application/vnd.oai.openapi"`)
		utils.WriteJSON(w, http.StatusNotFound, map[string]any{
			"error":              "unsupported API version",
			"supported_versions": []string{current},
		})
	})
}
