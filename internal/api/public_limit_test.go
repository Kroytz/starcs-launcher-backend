package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicBootstrapRateLimited(t *testing.T) {
	handler := newHandler()
	for i := 0; i < 60; i++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
		request.RemoteAddr = "203.0.113.10:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d %s", i+1, response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	request.RemoteAddr = "203.0.113.10:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d %s", response.Code, response.Body.String())
	}
	var body envelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 4014 {
		t.Fatalf("expected code 4014, got %d", body.Code)
	}
}
