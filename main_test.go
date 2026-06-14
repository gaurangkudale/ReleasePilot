package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	handler, err := newHandler(newStore())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}

func TestAnalyzeRelease(t *testing.T) {
	handler, err := newHandler(newStore())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(analyzeRequest{
		Repository:    "acme/platform",
		BaseBranch:    "main",
		ReleaseBranch: "hotfix/payment-retry",
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/analyze", bytes.NewReader(body)))

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}

	var result release
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Decision != "NO-GO" {
		t.Fatalf("expected NO-GO, got %s", result.Decision)
	}
}

func TestAnalyzeRequiresRepository(t *testing.T) {
	handler, err := newHandler(newStore())
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/analyze", bytes.NewBufferString(`{"baseBranch":"main","releaseBranch":"release/v1"}`)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}
