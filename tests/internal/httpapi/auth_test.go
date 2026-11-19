package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func authenticatedStatus(handler http.Handler, headers map[string]string) int {
	request := httptest.NewRequest(http.MethodGet, "/v1/indices", nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestTokenMiddlewareAcceptsBearerAndAPIKey(t *testing.T) {
	handler := newHandler(newFakeDatabase())
	for _, test := range []struct {
		name    string
		headers map[string]string
	}{
		{name: "bearer case insensitive", headers: map[string]string{"Authorization": "bEaReR test-token"}},
		{name: "bearer surrounding spaces", headers: map[string]string{"Authorization": "  Bearer   test-token  "}},
		{name: "api key fallback", headers: map[string]string{"X-API-Key": "test-token"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if status := authenticatedStatus(handler, test.headers); status != http.StatusOK {
				t.Fatalf("status = %d, want %d", status, http.StatusOK)
			}
		})
	}
}

func TestTokenMiddlewareRejectsInvalidCredentials(t *testing.T) {
	handler := newHandler(newFakeDatabase())
	for _, test := range []struct {
		name    string
		headers map[string]string
	}{
		{name: "missing", headers: nil},
		{name: "wrong bearer does not fall back to valid API key", headers: map[string]string{"Authorization": "Bearer wrong", "X-API-Key": "test-token"}},
		{name: "wrong api key", headers: map[string]string{"X-API-Key": "wrong"}},
		{name: "malformed bearer", headers: map[string]string{"Authorization": "test-token"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if status := authenticatedStatus(handler, test.headers); status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", status, http.StatusUnauthorized)
			}
		})
	}
}
