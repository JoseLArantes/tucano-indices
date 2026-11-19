package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func tokenMiddleware(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := strings.TrimSpace(r.Header.Get("Authorization"))
			if strings.HasPrefix(strings.ToLower(provided), "bearer ") {
				provided = strings.TrimSpace(provided[len("Bearer "):])
			} else {
				provided = strings.TrimSpace(r.Header.Get("X-API-Key"))
			}
			if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthorized", "A valid API token is required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
