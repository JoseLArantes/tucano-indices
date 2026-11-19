package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenMiddlewareAcceptsBearerAndAPIKey(t *testing.T) {
	middleware := tokenMiddleware("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, test := range []struct {
		name   string
		header string
		value  string
	}{
		{name: "bearer case insensitive", header: "Authorization", value: "bEaReR secret"},
		{name: "bearer surrounding spaces", header: "Authorization", value: "  Bearer   secret  "},
		{name: "api key fallback", header: "X-API-Key", value: "secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/private", nil)
			request.Header.Set(test.header, test.value)
			middleware(next).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
			}
		})
	}
}

func TestTokenMiddlewareRejectsInvalidCredentials(t *testing.T) {
	middleware := tokenMiddleware("secret")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Fatal("protected handler was called") })
	for _, test := range []struct {
		name   string
		header string
		value  string
		apiKey string
	}{
		{name: "missing"},
		{name: "wrong bearer does not fall back to valid API key", header: "Authorization", value: "Bearer wrong", apiKey: "secret"},
		{name: "wrong api key", header: "X-API-Key", value: "wrong"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/private", nil)
			if test.header != "" {
				request.Header.Set(test.header, test.value)
			}
			if test.apiKey != "" {
				request.Header.Set("X-API-Key", test.apiKey)
			}
			middleware(next).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
		})
	}
}
