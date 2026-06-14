package main

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFiles embed.FS

type validation struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type risk struct {
	Title    string `json:"title"`
	Level    string `json:"level"`
	Impact   string `json:"impact"`
	Evidence string `json:"evidence"`
}

type service struct {
	Name        string `json:"name"`
	Change      string `json:"change"`
	BlastRadius string `json:"blastRadius"`
}

type release struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Repository     string       `json:"repository"`
	BaseBranch     string       `json:"baseBranch"`
	ReleaseBranch  string       `json:"releaseBranch"`
	CreatedAt      time.Time    `json:"createdAt"`
	InitiatedBy    string       `json:"initiatedBy"`
	Target         string       `json:"target"`
	Decision       string       `json:"decision"`
	Recommendation string       `json:"recommendation"`
	Health         int          `json:"health"`
	Validations    []validation `json:"validations"`
	Risks          []risk       `json:"risks"`
	Services       []service    `json:"services"`
	RollbackPlan   []string     `json:"rollbackPlan"`
	ReleaseNotes   []string     `json:"releaseNotes"`
}

type analyzeRequest struct {
	Repository    string `json:"repository"`
	BaseBranch    string `json:"baseBranch"`
	ReleaseBranch string `json:"releaseBranch"`
}

type store struct {
	mu       sync.RWMutex
	releases map[string]release
}

func main() {
	app := newStore()
	handler, err := newHandler(app)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("ReleasePilot running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", handler))
}

func newStore() *store {
	s := &store{releases: make(map[string]release)}
	sample := analyzeRelease(analyzeRequest{
		Repository:    "acme/payment-platform",
		BaseBranch:    "main",
		ReleaseBranch: "release/v2.14.0",
	})
	s.releases[sample.ID] = sample
	return s
}

func newHandler(s *store) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/releases", s.handleReleases)
	mux.HandleFunc("/api/releases/", s.handleRelease)
	mux.HandleFunc("/api/analyze", s.handleAnalyze)

	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	mux.Handle("/", http.FileServer(http.FS(static)))
	return mux, nil
}

func (s *store) handleReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]release, 0, len(s.releases))
	for _, item := range s.releases {
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *store) handleRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/releases/")
	s.mu.RLock()
	item, ok := s.releases[id]
	s.mu.RUnlock()
	if !ok {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *store) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var input analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validateAnalyzeRequest(input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	item := analyzeRelease(input)
	s.mu.Lock()
	s.releases[item.ID] = item
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, item)
}

func validateAnalyzeRequest(input analyzeRequest) error {
	if strings.TrimSpace(input.Repository) == "" {
		return errors.New("repository is required")
	}
	if strings.TrimSpace(input.BaseBranch) == "" {
		return errors.New("base branch is required")
	}
	if strings.TrimSpace(input.ReleaseBranch) == "" {
		return errors.New("release branch is required")
	}
	return nil
}

func analyzeRelease(input analyzeRequest) release {
	now := time.Now().UTC()
	branchName := strings.TrimPrefix(input.ReleaseBranch, "release/")
	if branchName == input.ReleaseBranch {
		branchName = input.ReleaseBranch
	}

	decision := "NEEDS VALIDATION"
	health := 68
	recommendation := "Address high-risk findings and complete mandatory validations before release."
	if strings.Contains(strings.ToLower(input.ReleaseBranch), "hotfix") {
		decision = "NO-GO"
		health = 42
		recommendation = "Do not proceed until the database and payment retry blockers are resolved."
	}

	return release{
		ID:             "r-" + now.Format("20060102150405.000000"),
		Name:           "Payment Service " + branchName,
		Repository:     input.Repository,
		BaseBranch:     input.BaseBranch,
		ReleaseBranch:  input.ReleaseBranch,
		CreatedAt:      now,
		InitiatedBy:    "release.manager@acme.com",
		Target:         "Production",
		Decision:       decision,
		Recommendation: recommendation,
		Health:         health,
		Validations: []validation{
			{Name: "Build succeeded", Status: "Passed", Evidence: "CI run #1842"},
			{Name: "Unit and integration tests", Status: "Passed", Evidence: "1,248 tests"},
			{Name: "Security scans", Status: "Passed", Evidence: "No critical findings"},
			{Name: "Dependency checks", Status: "Passed", Evidence: "SBOM generated"},
			{Name: "Canary analysis (10%)", Status: "Warning", Evidence: "Latency p95 +12%"},
			{Name: "Rollback rehearsal", Status: "Pending", Evidence: "Not run"},
		},
		Risks: []risk{
			{Title: "Payment timeout reduced from 10s to 2s", Level: "High", Impact: "Customer experience", Evidence: "internal/payments/client.go:47"},
			{Title: "Database migration expands lock scope", Level: "High", Impact: "Availability", Evidence: "migrations/20240528_add_index.sql:12"},
			{Title: "New dependency: stripe-node v13.4.0", Level: "Medium", Impact: "Stability", Evidence: "go.mod:23"},
			{Title: "Feature flag defaults to on", Level: "Medium", Impact: "Change failure", Evidence: "config/defaults.yml:88"},
			{Title: "Concurrent index added safely", Level: "Low", Impact: "Performance", Evidence: "migrations/20240528_add_index.sql:18"},
		},
		Services: []service{
			{Name: "payment-service", Change: branchName, BlastRadius: "High"},
			{Name: "checkout-service", Change: "API consumer", BlastRadius: "High"},
			{Name: "billing-service", Change: "Indirect", BlastRadius: "Medium"},
			{Name: "notification-service", Change: "No change", BlastRadius: "Low"},
		},
		RollbackPlan: []string{
			"Disable feature flag: new-tax-calc",
			"Scale down payment-service to 0",
			"Revert payment-service to v2.13.2",
			"Run DB migration rollback: 20240528_rollback.sql",
			"Verify health checks and SLOs",
		},
		ReleaseNotes: []string{
			"Added tax calculation v2 with regional rules.",
			"Upgraded payment provider client.",
			"Improved idempotency handling for payment captures.",
			"Added metrics for payment latency.",
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
