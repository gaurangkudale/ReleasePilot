package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSafeOpenAIQuotaError(t *testing.T) {
	response := &http.Response{
		Status:     "429 Too Many Requests",
		StatusCode: http.StatusTooManyRequests,
		Body:       http.NoBody,
	}
	response.Body = ioNopCloser(`{"error":{"code":"insufficient_quota","message":"sensitive provider detail"}}`)
	err := safeOpenAIError(response)
	if !strings.Contains(err.Error(), "quota is exhausted") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatal("safe error exposed provider detail")
	}
}

func ioNopCloser(value string) *readCloser {
	return &readCloser{Reader: strings.NewReader(value)}
}

type readCloser struct {
	*strings.Reader
}

func (r *readCloser) Close() error { return nil }

func TestAnalyzeRelease(t *testing.T) {
	s := newStore()
	s.settings.OpenAIKey = ""
	handler, err := newHandler(s)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(analyzeRequest{
		Provider:      "demo",
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

func TestSettingsNeverReturnTokens(t *testing.T) {
	s := newStore()
	s.settings.GitHub = providerSecret{Username: "octocat", Token: "secret-token"}
	handler, err := newHandler(s)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings", nil))

	if bytes.Contains(response.Body.Bytes(), []byte("secret-token")) {
		t.Fatal("settings response exposed a stored token")
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"configured":true`)) {
		t.Fatal("settings response did not report configured token")
	}
}

func TestPDFExport(t *testing.T) {
	s := newStore()
	handler, err := newHandler(s)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for key := range s.releases {
		id = key
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/releases/"+id+"/pdf", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	if !bytes.HasPrefix(response.Body.Bytes(), []byte("%PDF-1.4")) {
		t.Fatal("response is not a PDF")
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
