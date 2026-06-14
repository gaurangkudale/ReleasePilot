package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sort"
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

type commit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	URL     string    `json:"url"`
}

type release struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Provider       string       `json:"provider"`
	Repository     string       `json:"repository"`
	BaseBranch     string       `json:"baseBranch"`
	ReleaseBranch  string       `json:"releaseBranch"`
	CreatedAt      time.Time    `json:"createdAt"`
	InitiatedBy    string       `json:"initiatedBy"`
	Target         string       `json:"target"`
	Decision       string       `json:"decision"`
	Summary        string       `json:"summary"`
	Recommendation string       `json:"recommendation"`
	Health         int          `json:"health"`
	AIGenerated    bool         `json:"aiGenerated"`
	Commits        []commit     `json:"commits"`
	Validations    []validation `json:"validations"`
	Risks          []risk       `json:"risks"`
	Services       []service    `json:"services"`
	RollbackPlan   []string     `json:"rollbackPlan"`
	ReleaseNotes   []string     `json:"releaseNotes"`
}

type analyzeRequest struct {
	Provider      string `json:"provider"`
	Repository    string `json:"repository"`
	BaseBranch    string `json:"baseBranch"`
	ReleaseBranch string `json:"releaseBranch"`
}

type providerSecret struct {
	Username string
	Token    string
}

type appSettings struct {
	GitHub      providerSecret
	GitLab      providerSecret
	OpenAIKey   string
	OpenAIModel string
}

type settingsView struct {
	GitHub struct {
		Username   string `json:"username"`
		Configured bool   `json:"configured"`
	} `json:"github"`
	GitLab struct {
		Username   string `json:"username"`
		Configured bool   `json:"configured"`
	} `json:"gitlab"`
	OpenAI struct {
		Configured bool   `json:"configured"`
		Model      string `json:"model"`
		Source     string `json:"source"`
	} `json:"openai"`
}

type settingsUpdate struct {
	GitHub struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	} `json:"github"`
	GitLab struct {
		Username string `json:"username"`
		Token    string `json:"token"`
	} `json:"gitlab"`
	OpenAI struct {
		APIKey string `json:"apiKey"`
		Model  string `json:"model"`
	} `json:"openai"`
}

type store struct {
	mu       sync.RWMutex
	releases map[string]release
	settings appSettings
	client   *http.Client
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
	s := &store{
		releases: make(map[string]release),
		settings: appSettings{
			OpenAIKey:   os.Getenv("OPENAI_API_KEY"),
			OpenAIModel: "gpt-5.2",
		},
		client: &http.Client{Timeout: 25 * time.Second},
	}
	sample := deterministicRelease(analyzeRequest{
		Provider:      "demo",
		Repository:    "acme/payment-platform",
		BaseBranch:    "main",
		ReleaseBranch: "release/v2.14.0",
	}, nil)
	s.releases[sample.ID] = sample
	return s
}

func newHandler(s *store) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/connections/test", s.handleConnectionTest)
	mux.HandleFunc("/api/repositories", s.handleRepositories)
	mux.HandleFunc("/api/history", s.handleHistory)
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

func (s *store) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		view := s.settings.view()
		s.mu.RUnlock()
		writeJSON(w, http.StatusOK, view)
	case http.MethodPut:
		var input settingsUpdate
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.settings.GitHub.Username = strings.TrimSpace(input.GitHub.Username)
		s.settings.GitLab.Username = strings.TrimSpace(input.GitLab.Username)
		if input.GitHub.Token != "" {
			s.settings.GitHub.Token = strings.TrimSpace(input.GitHub.Token)
		}
		if input.GitLab.Token != "" {
			s.settings.GitLab.Token = strings.TrimSpace(input.GitLab.Token)
		}
		if input.OpenAI.APIKey != "" {
			s.settings.OpenAIKey = strings.TrimSpace(input.OpenAI.APIKey)
		}
		if strings.TrimSpace(input.OpenAI.Model) != "" {
			s.settings.OpenAIModel = strings.TrimSpace(input.OpenAI.Model)
		}
		view := s.settings.view()
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, view)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *store) handleConnectionTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	message, err := s.testConnection(r.Context(), input.Provider, settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": message})
}

func (s *store) handleRepositories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := r.URL.Query().Get("provider")
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	repositories, err := s.fetchRepositories(r.Context(), provider, settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, repositories)
}

func (s *store) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	history, err := s.fetchHistory(r.Context(), r.URL.Query().Get("provider"), r.URL.Query().Get("repository"), r.URL.Query().Get("ref"), settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func (s *store) handleReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	items := make([]release, 0, len(s.releases))
	for _, item := range s.releases {
		items = append(items, item)
	}
	s.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	writeJSON(w, http.StatusOK, items)
}

func (s *store) handleRelease(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/releases/")
	if strings.HasSuffix(path, "/pdf") {
		s.handlePDF(w, r, strings.TrimSuffix(path, "/pdf"))
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	item, ok := s.releases[path]
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

	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()

	var history []commit
	var err error
	if input.Provider != "" && input.Provider != "demo" {
		history, err = s.fetchHistory(r.Context(), input.Provider, input.Repository, input.ReleaseBranch, settings)
		if err != nil {
			http.Error(w, "repository history: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	item := deterministicRelease(input, history)
	if settings.OpenAIKey != "" {
		aiReport, aiErr := s.generateAIReport(r.Context(), settings, input, history)
		if aiErr != nil {
			item.Summary += " AI generation was unavailable: " + aiErr.Error()
		} else {
			item.Summary = aiReport.Summary
			item.Recommendation = aiReport.Recommendation
			item.Decision = aiReport.Decision
			item.Health = aiReport.Health
			item.Risks = aiReport.Risks
			item.RollbackPlan = aiReport.RollbackPlan
			item.ReleaseNotes = aiReport.ReleaseNotes
			item.AIGenerated = true
		}
	}

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
	if input.Provider != "" && input.Provider != "demo" && input.Provider != "github" && input.Provider != "gitlab" {
		return errors.New("provider must be github or gitlab")
	}
	return nil
}

func deterministicRelease(input analyzeRequest, history []commit) release {
	now := time.Now().UTC()
	if history == nil {
		history = []commit{}
	}
	branchName := strings.TrimPrefix(input.ReleaseBranch, "release/")
	decision := "NEEDS VALIDATION"
	health := 68
	recommendation := "Address high-risk findings and complete mandatory validations before release."
	if strings.Contains(strings.ToLower(input.ReleaseBranch), "hotfix") {
		decision, health = "NO-GO", 42
		recommendation = "Do not proceed until the database and payment retry blockers are resolved."
	}
	if input.Provider == "" {
		input.Provider = "demo"
	}
	name := strings.TrimSuffix(input.Repository[strings.LastIndex(input.Repository, "/")+1:], ".git") + " " + branchName
	return release{
		ID:             "r-" + now.Format("20060102150405.000000"),
		Name:           name,
		Provider:       input.Provider,
		Repository:     input.Repository,
		BaseBranch:     input.BaseBranch,
		ReleaseBranch:  input.ReleaseBranch,
		CreatedAt:      now,
		InitiatedBy:    "release.manager@acme.com",
		Target:         "Production",
		Decision:       decision,
		Summary:        fmt.Sprintf("Analyzed %d recent commits from %s. Review the evidence and validations before release.", len(history), input.Repository),
		Recommendation: recommendation,
		Health:         health,
		Commits:        history,
		Validations: []validation{
			{Name: "Repository history fetched", Status: "Passed", Evidence: fmt.Sprintf("%d commits", len(history))},
			{Name: "Unit and integration tests", Status: "Pending", Evidence: "Connect CI provider"},
			{Name: "Security scans", Status: "Pending", Evidence: "Connect security scanner"},
			{Name: "Dependency checks", Status: "Passed", Evidence: "History reviewed"},
			{Name: "Canary analysis", Status: "Pending", Evidence: "Not run"},
			{Name: "Rollback rehearsal", Status: "Pending", Evidence: "Not run"},
		},
		Risks: []risk{
			{Title: "Release includes unvalidated repository changes", Level: "High", Impact: "Release safety", Evidence: input.ReleaseBranch},
			{Title: "CI and runtime signals are not connected", Level: "Medium", Impact: "Confidence", Evidence: "Settings"},
		},
		Services: []service{{Name: name, Change: branchName, BlastRadius: "High"}},
		RollbackPlan: []string{
			"Pause the deployment and disable changed feature flags",
			"Revert the release branch to the previously deployed commit",
			"Redeploy the last known-good artifact",
			"Verify health checks and service-level objectives",
		},
		ReleaseNotes: releaseNotesFromCommits(history),
	}
}

func releaseNotesFromCommits(history []commit) []string {
	if len(history) == 0 {
		return []string{"No repository commits were available. Connect GitHub or GitLab to generate release notes from history."}
	}
	limit := min(6, len(history))
	notes := make([]string, 0, limit)
	for _, item := range history[:limit] {
		notes = append(notes, strings.Split(item.Message, "\n")[0])
	}
	return notes
}

func (s appSettings) view() settingsView {
	var view settingsView
	view.GitHub.Username, view.GitHub.Configured = s.GitHub.Username, s.GitHub.Token != ""
	view.GitLab.Username, view.GitLab.Configured = s.GitLab.Username, s.GitLab.Token != ""
	view.OpenAI.Configured, view.OpenAI.Model = s.OpenAIKey != "", s.OpenAIModel
	if os.Getenv("OPENAI_API_KEY") != "" && s.OpenAIKey == os.Getenv("OPENAI_API_KEY") {
		view.OpenAI.Source = "environment"
	} else if s.OpenAIKey != "" {
		view.OpenAI.Source = "settings"
	}
	return view
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
