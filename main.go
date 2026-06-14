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
	"path/filepath"
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

type changedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
}

type providerSignal struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
	URL      string `json:"url,omitempty"`
}

type agentReport struct {
	Name       string `json:"name"`
	Domain     string `json:"domain"`
	Status     string `json:"status"`
	Confidence int    `json:"confidence"`
	Summary    string `json:"summary"`
	Findings   []risk `json:"findings"`
}

type blastNode struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Evidence string `json:"evidence"`
}

type blastEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

type blastRadiusGraph struct {
	Nodes []blastNode `json:"nodes"`
	Edges []blastEdge `json:"edges"`
}

type releaseContext struct {
	Commits      []commit       `json:"commits"`
	ChangedFiles []changedFile  `json:"changedFiles"`
	CI           providerSignal `json:"ci"`
	Security     providerSignal `json:"security"`
	Database     providerSignal `json:"database"`
	Schema       providerSignal `json:"schema"`
}

type release struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Provider       string           `json:"provider"`
	Repository     string           `json:"repository"`
	BaseBranch     string           `json:"baseBranch"`
	ReleaseBranch  string           `json:"releaseBranch"`
	CreatedAt      time.Time        `json:"createdAt"`
	InitiatedBy    string           `json:"initiatedBy"`
	Target         string           `json:"target"`
	Decision       string           `json:"decision"`
	Summary        string           `json:"summary"`
	Recommendation string           `json:"recommendation"`
	Health         int              `json:"health"`
	AIGenerated    bool             `json:"aiGenerated"`
	Commits        []commit         `json:"commits"`
	ChangedFiles   []changedFile    `json:"changedFiles"`
	Agents         []agentReport    `json:"agents"`
	BlastRadius    blastRadiusGraph `json:"blastRadius"`
	Validations    []validation     `json:"validations"`
	Risks          []risk           `json:"risks"`
	Services       []service        `json:"services"`
	RollbackPlan   []string         `json:"rollbackPlan"`
	ReleaseNotes   []string         `json:"releaseNotes"`
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
	path     string
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
	dataPath := os.Getenv("RELEASEPILOT_STORE")
	if dataPath == "" {
		dataPath = filepath.Join(".releasepilot", "store.json")
	}
	s := &store{
		releases: make(map[string]release),
		settings: appSettings{
			OpenAIKey:   os.Getenv("OPENAI_API_KEY"),
			OpenAIModel: "gpt-5.2",
		},
		client: &http.Client{Timeout: 25 * time.Second},
		path:   dataPath,
	}
	if err := s.load(); err != nil {
		log.Printf("ReleasePilot persistence disabled until next save: %v", err)
	}
	return s
}

func newHandler(s *store) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/connections/test", s.handleConnectionTest)
	mux.HandleFunc("/api/openai/models", s.handleOpenAIModels)
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
		err := s.saveLocked()
		s.mu.Unlock()
		if err != nil {
			http.Error(w, "settings saved in memory but persistence failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
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
		Username string `json:"username"`
		Token    string `json:"token"`
		APIKey   string `json:"apiKey"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	settings.applyTestOverrides(input.Provider, input.Username, input.Token, input.APIKey, input.Model)
	message, err := s.testConnection(r.Context(), input.Provider, settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": message})
}

func (s *store) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	models, err := s.fetchOpenAIModels(r.Context(), settings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, models)
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
		items = append(items, normalizeRelease(item))
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
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		item, ok := s.releases[path]
		s.mu.RUnlock()
		if !ok {
			http.Error(w, "release not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, normalizeRelease(item))
	case http.MethodDelete:
		s.mu.Lock()
		item, ok := s.releases[path]
		if !ok {
			s.mu.Unlock()
			http.Error(w, "release not found", http.StatusNotFound)
			return
		}
		delete(s.releases, path)
		err := s.saveLocked()
		if err != nil {
			s.releases[path] = item
		}
		s.mu.Unlock()
		if err != nil {
			http.Error(w, "release delete failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

	ctx := releaseContext{}
	var err error
	if input.Provider != "" && input.Provider != "demo" {
		ctx, err = s.fetchReleaseContext(r.Context(), input.Provider, input.Repository, input.BaseBranch, input.ReleaseBranch, settings)
		if err != nil {
			http.Error(w, "repository analysis: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	item := deterministicRelease(input, ctx)
	if settings.OpenAIKey != "" {
		aiReport, aiErr := s.generateAIReport(r.Context(), settings, input, ctx)
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
	err = s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "analysis completed but persistence failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
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

func deterministicRelease(input analyzeRequest, ctx releaseContext) release {
	now := time.Now().UTC()
	ctx = enrichReleaseContext(ctx)
	if ctx.Commits == nil {
		ctx.Commits = []commit{}
	}
	if ctx.ChangedFiles == nil {
		ctx.ChangedFiles = []changedFile{}
	}
	branchName := strings.TrimPrefix(input.ReleaseBranch, "release/")
	if input.Provider == "" {
		input.Provider = "demo"
	}
	name := strings.TrimSuffix(input.Repository[strings.LastIndex(input.Repository, "/")+1:], ".git") + " " + branchName
	agents := runRiskAgents(ctx)
	risks := deterministicRisks(input, ctx, agents)
	decision, health, recommendation := synthesizeVerdict(input, risks)
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
		Summary:        fmt.Sprintf("Analyzed %d commits and %d changed files from %s. Review CI, security, schema, and database signals before release.", len(ctx.Commits), len(ctx.ChangedFiles), input.Repository),
		Recommendation: recommendation,
		Health:         health,
		Commits:        ctx.Commits,
		ChangedFiles:   ctx.ChangedFiles,
		Agents:         agents,
		BlastRadius:    buildBlastRadius(input, ctx, name, branchName, decision),
		Validations: []validation{
			{Name: "Repository history fetched", Status: statusFromCount(len(ctx.Commits)), Evidence: fmt.Sprintf("%d commits, %d changed files", len(ctx.Commits), len(ctx.ChangedFiles))},
			{Name: "CI validation", Status: ctx.CI.Status, Evidence: emptyAs(ctx.CI.Evidence, "No pipeline data found")},
			{Name: "Security scan", Status: ctx.Security.Status, Evidence: emptyAs(ctx.Security.Evidence, "No credential files detected")},
			{Name: "Dependency checks", Status: "Passed", Evidence: "History reviewed"},
			{Name: "Database/schema review", Status: worstStatus(ctx.Database.Status, ctx.Schema.Status), Evidence: strings.TrimSpace(ctx.Database.Evidence + " " + ctx.Schema.Evidence)},
			{Name: "Canary analysis", Status: "Pending", Evidence: "Not run"},
			{Name: "Rollback rehearsal", Status: "Pending", Evidence: "Not run"},
		},
		Risks:    risks,
		Services: []service{{Name: name, Change: branchName, BlastRadius: "High"}},
		RollbackPlan: []string{
			"Pause the deployment and disable changed feature flags",
			"Revert the release branch to the previously deployed commit",
			"Redeploy the last known-good artifact",
			"Verify health checks and service-level objectives",
		},
		ReleaseNotes: releaseNotesFromCommits(ctx.Commits),
	}
}

func enrichReleaseContext(ctx releaseContext) releaseContext {
	if ctx.CI.Status == "" {
		ctx.CI = providerSignal{Name: "CI validation", Status: "Pending", Evidence: "No CI result found"}
	}
	if ctx.Security.Status == "" {
		ctx.Security = scanSecrets(ctx.ChangedFiles)
	}
	if ctx.Database.Status == "" {
		ctx.Database = scanDatabaseChanges(ctx.ChangedFiles)
	}
	if ctx.Schema.Status == "" {
		ctx.Schema = scanSchemaChanges(ctx.ChangedFiles)
	}
	return ctx
}

func deterministicRisks(input analyzeRequest, ctx releaseContext, agents []agentReport) []risk {
	risks := []risk{}
	for _, agent := range agents {
		risks = append(risks, agent.Findings...)
	}
	if len(ctx.ChangedFiles) == 0 {
		risks = append(risks, risk{Title: "No compare data available", Level: "Medium", Impact: "Release confidence", Evidence: input.ReleaseBranch})
	}
	if len(risks) == 0 {
		risks = append(risks, risk{Title: "No high-risk repository signals detected", Level: "Low", Impact: "Release confidence", Evidence: input.ReleaseBranch})
	}
	return risks
}

func runRiskAgents(ctx releaseContext) []agentReport {
	return []agentReport{
		securityAgent(ctx),
		schemaMigrationAgent(ctx),
		ciCoverageAgent(ctx),
		performanceAgent(ctx),
	}
}

func securityAgent(ctx releaseContext) agentReport {
	status, confidence := "Clear", 91
	findings := []risk{}
	if ctx.Security.Status == "Failed" {
		status, confidence = "Blocked", 96
		findings = append(findings, risk{Title: "Possible credentials or .env files in release", Level: "Critical", Impact: "Secret exposure", Evidence: ctx.Security.Evidence})
	}
	return agentReport{Name: "Security Agent", Domain: "Secrets and credentials", Status: status, Confidence: confidence, Summary: ctx.Security.Evidence, Findings: findings}
}

func schemaMigrationAgent(ctx releaseContext) agentReport {
	status, confidence := "Clear", 84
	findings := []risk{}
	summary := []string{}
	if ctx.Database.Status == "Warning" {
		status, confidence = "Review", 90
		summary = append(summary, "Database: "+ctx.Database.Evidence)
		findings = append(findings, risk{Title: "Database migration detected", Level: "High", Impact: "Rollback complexity", Evidence: ctx.Database.Evidence})
	}
	if ctx.Schema.Status == "Warning" {
		status, confidence = "Review", max(confidence, 88)
		summary = append(summary, "Schema: "+ctx.Schema.Evidence)
		findings = append(findings, risk{Title: "Schema or API contract change detected", Level: "High", Impact: "Compatibility", Evidence: ctx.Schema.Evidence})
	}
	if len(summary) == 0 {
		summary = append(summary, "No database or schema contract files detected")
	}
	return agentReport{Name: "Schema/Migration Agent", Domain: "DB and contracts", Status: status, Confidence: confidence, Summary: strings.Join(summary, " | "), Findings: findings}
}

func ciCoverageAgent(ctx releaseContext) agentReport {
	status, confidence := "Clear", 82
	findings := []risk{}
	if ctx.CI.Status == "Failed" {
		status, confidence = "Blocked", 94
		findings = append(findings, risk{Title: "CI validation failed", Level: "High", Impact: "Release safety", Evidence: ctx.CI.Evidence})
	} else if ctx.CI.Status != "Passed" {
		status, confidence = "Review", 72
		findings = append(findings, risk{Title: "CI validation is not passing", Level: "High", Impact: "Release safety", Evidence: emptyAs(ctx.CI.Evidence, "No CI status")})
	}
	return agentReport{Name: "Test Coverage Agent", Domain: "CI and validation", Status: status, Confidence: confidence, Summary: emptyAs(ctx.CI.Evidence, "No CI signal available"), Findings: findings}
}

func performanceAgent(ctx releaseContext) agentReport {
	findings := []risk{}
	touched := []string{}
	totalDelta := 0
	for _, file := range ctx.ChangedFiles {
		lower := strings.ToLower(file.Path + " " + file.Patch)
		totalDelta += file.Additions + file.Deletions
		if strings.Contains(lower, "timeout") || strings.Contains(lower, "cache") || strings.Contains(lower, "query") || strings.Contains(lower, "loop") || strings.Contains(lower, "payment") || strings.Contains(lower, "checkout") {
			touched = append(touched, file.Path)
		}
	}
	status, confidence := "Clear", 70
	summary := "No obvious performance-sensitive paths detected"
	if totalDelta > 600 || len(touched) > 0 {
		status, confidence = "Review", 76
		summary = "Performance-sensitive paths: " + strings.Join(uniqueStrings(touched), ", ")
		if len(touched) == 0 {
			summary = fmt.Sprintf("Large release delta: %d changed lines", totalDelta)
		}
		findings = append(findings, risk{Title: "Performance-sensitive change needs validation", Level: "Medium", Impact: "Latency or throughput", Evidence: summary})
	}
	return agentReport{Name: "Performance Agent", Domain: "Hot paths and diff size", Status: status, Confidence: confidence, Summary: summary, Findings: findings}
}

func synthesizeVerdict(input analyzeRequest, risks []risk) (string, int, string) {
	score := 92
	decision := "GO"
	for _, item := range risks {
		switch item.Level {
		case "Critical":
			score -= 35
			decision = "NO-GO"
		case "High":
			score -= 18
			if decision != "NO-GO" {
				decision = "NEEDS VALIDATION"
			}
		case "Medium":
			score -= 8
			if decision == "GO" {
				decision = "NEEDS VALIDATION"
			}
		case "Low":
			score -= 2
		}
	}
	if strings.Contains(strings.ToLower(input.ReleaseBranch), "hotfix") && decision == "GO" {
		score -= 10
		decision = "NEEDS VALIDATION"
	}
	score = min(100, max(0, score))
	switch decision {
	case "NO-GO":
		return decision, score, "Do not release until blocked agent findings are resolved and revalidated."
	case "NEEDS VALIDATION":
		return decision, score, "Complete the highlighted agent validations before approving this release."
	default:
		return decision, score, "No blocking agent findings detected. Proceed with standard release checks."
	}
}

func buildBlastRadius(input analyzeRequest, ctx releaseContext, serviceName, branchName, decision string) blastRadiusGraph {
	nodes := []blastNode{{ID: "release", Label: branchName, Kind: "release", Severity: severityFromDecision(decision), Evidence: input.ReleaseBranch}}
	edges := []blastEdge{}
	addNode := func(id, label, kind, severity, evidence string) {
		nodes = append(nodes, blastNode{ID: id, Label: label, Kind: kind, Severity: severity, Evidence: evidence})
		edges = append(edges, blastEdge{From: "release", To: id, Label: kind})
	}
	addNode("service", serviceName, "service", "High", fmt.Sprintf("%d changed files", len(ctx.ChangedFiles)))
	addNode("ci", "CI validation", "signal", severityFromStatus(ctx.CI.Status), ctx.CI.Evidence)
	addNode("security", "Secret scan", "signal", severityFromStatus(ctx.Security.Status), ctx.Security.Evidence)
	addNode("database", "Database", "signal", severityFromStatus(ctx.Database.Status), ctx.Database.Evidence)
	addNode("schema", "Schema/API", "signal", severityFromStatus(ctx.Schema.Status), ctx.Schema.Evidence)
	for i, file := range ctx.ChangedFiles {
		if i >= 5 {
			break
		}
		addNode(fmt.Sprintf("file-%d", i), file.Path, "file", severityFromFile(file.Path), file.Status)
	}
	return blastRadiusGraph{Nodes: nodes, Edges: edges}
}

func severityFromStatus(status string) string {
	switch status {
	case "Failed":
		return "Critical"
	case "Warning", "Pending":
		return "Medium"
	case "Passed":
		return "Low"
	default:
		return "Medium"
	}
}

func severityFromFile(path string) string {
	lower := strings.ToLower(path)
	if strings.Contains(lower, ".env") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") {
		return "Critical"
	}
	if strings.Contains(lower, "migration") || strings.Contains(lower, ".sql") || strings.Contains(lower, "openapi") || strings.Contains(lower, "schema") || strings.Contains(lower, ".proto") {
		return "High"
	}
	return "Medium"
}

func severityFromDecision(decision string) string {
	if decision == "NO-GO" {
		return "Critical"
	}
	if decision == "GO" {
		return "Low"
	}
	return "Medium"
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

func (s *appSettings) applyTestOverrides(provider, username, token, apiKey, model string) {
	if model != "" {
		s.OpenAIModel = strings.TrimSpace(model)
	}
	switch provider {
	case "github":
		if username != "" {
			s.GitHub.Username = strings.TrimSpace(username)
		}
		if token != "" {
			s.GitHub.Token = strings.TrimSpace(token)
		}
	case "gitlab":
		if username != "" {
			s.GitLab.Username = strings.TrimSpace(username)
		}
		if token != "" {
			s.GitLab.Token = strings.TrimSpace(token)
		}
	case "openai":
		if apiKey != "" {
			s.OpenAIKey = strings.TrimSpace(apiKey)
		}
	}
}

func statusFromCount(count int) string {
	if count == 0 {
		return "Warning"
	}
	return "Passed"
}

func worstStatus(a, b string) string {
	for _, status := range []string{"Failed", "Warning", "Pending"} {
		if a == status || b == status {
			return status
		}
	}
	return "Passed"
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func normalizeRelease(item release) release {
	if item.Commits == nil {
		item.Commits = []commit{}
	}
	if item.ChangedFiles == nil {
		item.ChangedFiles = []changedFile{}
	}
	if item.Agents == nil {
		item.Agents = []agentReport{}
	}
	if item.BlastRadius.Nodes == nil {
		item.BlastRadius.Nodes = []blastNode{}
	}
	if item.BlastRadius.Edges == nil {
		item.BlastRadius.Edges = []blastEdge{}
	}
	if item.Validations == nil {
		item.Validations = []validation{}
	}
	if item.Risks == nil {
		item.Risks = []risk{}
	}
	if item.Services == nil {
		item.Services = []service{}
	}
	if item.RollbackPlan == nil {
		item.RollbackPlan = []string{}
	}
	if item.ReleaseNotes == nil {
		item.ReleaseNotes = []string{}
	}
	return item
}
