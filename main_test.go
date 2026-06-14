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
	handler, err := newHandler(testStore(t))
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
	s := testStore(t)
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
	s := testStore(t)
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
	s := testStore(t)
	item := deterministicRelease(analyzeRequest{Provider: "github", Repository: "acme/platform", BaseBranch: "main", ReleaseBranch: "release/v1"}, releaseContext{})
	s.releases[item.ID] = item
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
	handler, err := newHandler(testStore(t))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/analyze", bytes.NewBufferString(`{"baseBranch":"main","releaseBranch":"release/v1"}`)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}

func TestNewStoreDoesNotSeedDemoReports(t *testing.T) {
	s := testStore(t)
	if len(s.releases) != 0 {
		t.Fatalf("expected no seeded reports, got %d", len(s.releases))
	}
}

func TestReportsPersistAndReload(t *testing.T) {
	path := t.TempDir() + "/store.json"
	t.Setenv("RELEASEPILOT_STORE", path)
	s := newStore()
	item := deterministicRelease(analyzeRequest{Provider: "github", Repository: "acme/platform", BaseBranch: "main", ReleaseBranch: "release/v1"}, releaseContext{})
	s.releases[item.ID] = item
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	reloaded := newStore()
	if _, ok := reloaded.releases[item.ID]; !ok {
		t.Fatal("expected persisted release after reload")
	}
}

func TestDemoReportsFilteredOnLoad(t *testing.T) {
	path := t.TempDir() + "/store.json"
	t.Setenv("RELEASEPILOT_STORE", path)
	s := newStore()
	demo := deterministicRelease(analyzeRequest{Provider: "demo", Repository: "acme/platform", BaseBranch: "main", ReleaseBranch: "release/v1"}, releaseContext{})
	real := deterministicRelease(analyzeRequest{Provider: "github", Repository: "acme/platform", BaseBranch: "main", ReleaseBranch: "release/v1"}, releaseContext{})
	s.releases[demo.ID] = demo
	s.releases[real.ID] = real
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	reloaded := newStore()
	if _, ok := reloaded.releases[demo.ID]; ok {
		t.Fatal("demo release should be filtered on load")
	}
	if _, ok := reloaded.releases[real.ID]; !ok {
		t.Fatal("real release should remain on load")
	}
}

func TestRepositorySignalDetection(t *testing.T) {
	ctx := releaseContext{ChangedFiles: []changedFile{
		{Path: ".env.production", Patch: "+OPENAI_API_KEY=abc"},
		{Path: "migrations/001_create_users.sql"},
		{Path: "api/openapi.yaml"},
	}}
	report := deterministicRelease(analyzeRequest{Provider: "github", Repository: "acme/platform", BaseBranch: "main", ReleaseBranch: "release/v1"}, ctx)
	joined, _ := json.Marshal(report)
	for _, want := range []string{"Possible credentials", "Database migration", "Schema or API contract"} {
		if !bytes.Contains(joined, []byte(want)) {
			t.Fatalf("expected report to include %q: %s", want, joined)
		}
	}
}

func testStore(t *testing.T) *store {
	t.Helper()
	t.Setenv("RELEASEPILOT_STORE", t.TempDir()+"/store.json")
	return newStore()
}
