package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type repositoryOption struct {
	Provider      string `json:"provider"`
	FullName      string `json:"fullName"`
	DefaultBranch string `json:"defaultBranch"`
	Private       bool   `json:"private"`
	URL           string `json:"url"`
}

type openAIModel struct {
	ID string `json:"id"`
}

func (s *store) testConnection(ctx context.Context, provider string, settings appSettings) (string, error) {
	switch provider {
	case "github":
		var user struct {
			Login string `json:"login"`
		}
		if err := s.providerJSON(ctx, "https://api.github.com/user", settings.GitHub.Token, "github", &user); err != nil {
			return "", err
		}
		return "Connected to GitHub as " + user.Login, nil
	case "gitlab":
		var user struct {
			Username string `json:"username"`
		}
		if err := s.providerJSON(ctx, "https://gitlab.com/api/v4/user", settings.GitLab.Token, "gitlab", &user); err != nil {
			return "", err
		}
		return "Connected to GitLab as " + user.Username, nil
	case "openai":
		if settings.OpenAIKey == "" {
			return "", errors.New("OpenAI API key is not configured")
		}
		if _, err := s.fetchOpenAIModels(ctx, settings); err != nil {
			return "", err
		}
		return "OpenAI is configured and models are available", nil
	default:
		return "", errors.New("unknown provider")
	}
}

func (s *store) fetchOpenAIModels(ctx context.Context, settings appSettings) ([]openAIModel, error) {
	if settings.OpenAIKey == "" {
		return nil, errors.New("OpenAI API key is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+settings.OpenAIKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, safeOpenAIError(resp)
	}
	var payload struct {
		Data []openAIModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]openAIModel, 0, len(payload.Data))
	for _, model := range payload.Data {
		if strings.Contains(model.ID, "gpt") || strings.Contains(model.ID, "o") {
			models = append(models, model)
		}
	}
	return models, nil
}

func (s *store) fetchRepositories(ctx context.Context, provider string, settings appSettings) ([]repositoryOption, error) {
	switch provider {
	case "github":
		var response []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
			HTMLURL       string `json:"html_url"`
		}
		if err := s.providerJSON(ctx, "https://api.github.com/user/repos?per_page=100&sort=updated", settings.GitHub.Token, "github", &response); err != nil {
			return nil, err
		}
		result := make([]repositoryOption, 0, len(response))
		for _, repo := range response {
			result = append(result, repositoryOption{Provider: provider, FullName: repo.FullName, DefaultBranch: repo.DefaultBranch, Private: repo.Private, URL: repo.HTMLURL})
		}
		return result, nil
	case "gitlab":
		var response []struct {
			PathWithNamespace string `json:"path_with_namespace"`
			DefaultBranch     string `json:"default_branch"`
			Visibility        string `json:"visibility"`
			WebURL            string `json:"web_url"`
		}
		if err := s.providerJSON(ctx, "https://gitlab.com/api/v4/projects?membership=true&per_page=100&order_by=last_activity_at", settings.GitLab.Token, "gitlab", &response); err != nil {
			return nil, err
		}
		result := make([]repositoryOption, 0, len(response))
		for _, repo := range response {
			result = append(result, repositoryOption{Provider: provider, FullName: repo.PathWithNamespace, DefaultBranch: repo.DefaultBranch, Private: repo.Visibility != "public", URL: repo.WebURL})
		}
		return result, nil
	default:
		return nil, errors.New("provider must be github or gitlab")
	}
}

func (s *store) fetchHistory(ctx context.Context, provider, repository, ref string, settings appSettings) ([]commit, error) {
	if strings.TrimSpace(repository) == "" {
		return nil, errors.New("repository is required")
	}
	switch provider {
	case "github":
		endpoint := "https://api.github.com/repos/" + strings.Trim(repository, "/") + "/commits?per_page=30"
		if ref != "" {
			endpoint += "&sha=" + url.QueryEscape(ref)
		}
		var response []struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
			Commit  struct {
				Message string `json:"message"`
				Author  struct {
					Name string    `json:"name"`
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		}
		if err := s.providerJSON(ctx, endpoint, settings.GitHub.Token, "github", &response); err != nil {
			return nil, err
		}
		result := make([]commit, 0, len(response))
		for _, item := range response {
			result = append(result, commit{SHA: item.SHA, Message: item.Commit.Message, Author: item.Commit.Author.Name, Date: item.Commit.Author.Date, URL: item.HTMLURL})
		}
		return result, nil
	case "gitlab":
		endpoint := "https://gitlab.com/api/v4/projects/" + url.PathEscape(repository) + "/repository/commits?per_page=30"
		if ref != "" {
			endpoint += "&ref_name=" + url.QueryEscape(ref)
		}
		var response []struct {
			ID         string    `json:"id"`
			Title      string    `json:"title"`
			Message    string    `json:"message"`
			AuthorName string    `json:"author_name"`
			CreatedAt  time.Time `json:"created_at"`
			WebURL     string    `json:"web_url"`
		}
		if err := s.providerJSON(ctx, endpoint, settings.GitLab.Token, "gitlab", &response); err != nil {
			return nil, err
		}
		result := make([]commit, 0, len(response))
		for _, item := range response {
			result = append(result, commit{SHA: item.ID, Message: item.Message, Author: item.AuthorName, Date: item.CreatedAt, URL: item.WebURL})
		}
		return result, nil
	default:
		return nil, errors.New("provider must be github or gitlab")
	}
}

func (s *store) fetchReleaseContext(ctx context.Context, provider, repository, baseRef, releaseRef string, settings appSettings) (releaseContext, error) {
	history, err := s.fetchHistory(ctx, provider, repository, releaseRef, settings)
	if err != nil {
		return releaseContext{}, err
	}
	files, err := s.fetchChangedFiles(ctx, provider, repository, baseRef, releaseRef, settings)
	if err != nil {
		return releaseContext{}, err
	}
	ctxData := enrichReleaseContext(releaseContext{
		Commits:      history,
		ChangedFiles: files,
	})
	ci, err := s.fetchCIStatus(ctx, provider, repository, releaseRef, settings)
	if err == nil {
		ctxData.CI = ci
	}
	return ctxData, nil
}

func (s *store) fetchChangedFiles(ctx context.Context, provider, repository, baseRef, releaseRef string, settings appSettings) ([]changedFile, error) {
	switch provider {
	case "github":
		endpoint := "https://api.github.com/repos/" + strings.Trim(repository, "/") + "/compare/" + url.PathEscape(baseRef) + "..." + url.PathEscape(releaseRef)
		var response struct {
			Files []struct {
				Filename  string `json:"filename"`
				Status    string `json:"status"`
				Additions int    `json:"additions"`
				Deletions int    `json:"deletions"`
				Patch     string `json:"patch"`
			} `json:"files"`
		}
		if err := s.providerJSON(ctx, endpoint, settings.GitHub.Token, "github", &response); err != nil {
			return nil, err
		}
		files := make([]changedFile, 0, len(response.Files))
		for _, f := range response.Files {
			files = append(files, changedFile{Path: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions, Patch: f.Patch})
		}
		return files, nil
	case "gitlab":
		endpoint := "https://gitlab.com/api/v4/projects/" + url.PathEscape(repository) + "/repository/compare?from=" + url.QueryEscape(baseRef) + "&to=" + url.QueryEscape(releaseRef)
		var response struct {
			Diffs []struct {
				NewPath string `json:"new_path"`
				OldPath string `json:"old_path"`
				Diff    string `json:"diff"`
				NewFile bool   `json:"new_file"`
				Deleted bool   `json:"deleted_file"`
			} `json:"diffs"`
		}
		if err := s.providerJSON(ctx, endpoint, settings.GitLab.Token, "gitlab", &response); err != nil {
			return nil, err
		}
		files := make([]changedFile, 0, len(response.Diffs))
		for _, f := range response.Diffs {
			path := f.NewPath
			if path == "" {
				path = f.OldPath
			}
			status := "modified"
			if f.NewFile {
				status = "added"
			}
			if f.Deleted {
				status = "deleted"
			}
			files = append(files, changedFile{Path: path, Status: status, Patch: f.Diff})
		}
		return files, nil
	default:
		return nil, errors.New("provider must be github or gitlab")
	}
}

func (s *store) fetchCIStatus(ctx context.Context, provider, repository, ref string, settings appSettings) (providerSignal, error) {
	switch provider {
	case "github":
		endpoint := "https://api.github.com/repos/" + strings.Trim(repository, "/") + "/actions/runs?branch=" + url.QueryEscape(ref) + "&per_page=1"
		var response struct {
			WorkflowRuns []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				HTMLURL    string `json:"html_url"`
			} `json:"workflow_runs"`
		}
		if err := s.providerJSON(ctx, endpoint, settings.GitHub.Token, "github", &response); err != nil {
			return providerSignal{}, err
		}
		if len(response.WorkflowRuns) == 0 {
			return providerSignal{Name: "GitHub Actions", Status: "Pending", Evidence: "No workflow run found for " + ref}, nil
		}
		run := response.WorkflowRuns[0]
		return providerSignal{Name: "GitHub Actions", Status: ciStatus(run.Status, run.Conclusion), Evidence: run.Name + " " + emptyAs(run.Conclusion, run.Status), URL: run.HTMLURL}, nil
	case "gitlab":
		endpoint := "https://gitlab.com/api/v4/projects/" + url.PathEscape(repository) + "/pipelines?ref=" + url.QueryEscape(ref) + "&per_page=1"
		var response []struct {
			Status string `json:"status"`
			WebURL string `json:"web_url"`
		}
		if err := s.providerJSON(ctx, endpoint, settings.GitLab.Token, "gitlab", &response); err != nil {
			return providerSignal{}, err
		}
		if len(response) == 0 {
			return providerSignal{Name: "GitLab CI", Status: "Pending", Evidence: "No pipeline found for " + ref}, nil
		}
		return providerSignal{Name: "GitLab CI", Status: ciStatus(response[0].Status, response[0].Status), Evidence: "Pipeline " + response[0].Status, URL: response[0].WebURL}, nil
	default:
		return providerSignal{}, errors.New("provider must be github or gitlab")
	}
}

func scanSecrets(files []changedFile) providerSignal {
	matches := []string{}
	for _, file := range files {
		lower := strings.ToLower(file.Path)
		patch := strings.ToLower(file.Patch)
		if strings.Contains(lower, ".env") || strings.Contains(lower, "secret") || strings.Contains(lower, "credential") ||
			strings.Contains(patch, "api_key") || strings.Contains(patch, "apikey") || strings.Contains(patch, "password=") ||
			strings.Contains(patch, "private_key") || strings.Contains(patch, "secret") {
			matches = append(matches, file.Path)
		}
	}
	if len(matches) == 0 {
		return providerSignal{Name: "Secret scan", Status: "Passed", Evidence: "No .env or credential-like changes detected"}
	}
	return providerSignal{Name: "Secret scan", Status: "Failed", Evidence: strings.Join(uniqueStrings(matches), ", ")}
}

func scanDatabaseChanges(files []changedFile) providerSignal {
	matches := pathsMatching(files, []string{"migration", "migrations/", ".sql", "schema.sql", "db/", "database/"})
	if len(matches) == 0 {
		return providerSignal{Name: "Database changes", Status: "Passed", Evidence: "No database migration files detected"}
	}
	return providerSignal{Name: "Database changes", Status: "Warning", Evidence: strings.Join(matches, ", ")}
}

func scanSchemaChanges(files []changedFile) providerSignal {
	matches := pathsMatching(files, []string{"openapi", "swagger", "schema", "graphql", ".proto", "api/"})
	if len(matches) == 0 {
		return providerSignal{Name: "Schema changes", Status: "Passed", Evidence: "No API/schema files detected"}
	}
	return providerSignal{Name: "Schema changes", Status: "Warning", Evidence: strings.Join(matches, ", ")}
}

func pathsMatching(files []changedFile, needles []string) []string {
	matches := []string{}
	for _, file := range files {
		lower := strings.ToLower(file.Path)
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				matches = append(matches, file.Path)
				break
			}
		}
	}
	return uniqueStrings(matches)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func ciStatus(status, conclusion string) string {
	status = strings.ToLower(status)
	conclusion = strings.ToLower(conclusion)
	if conclusion == "success" || status == "success" {
		return "Passed"
	}
	if conclusion == "failure" || conclusion == "cancelled" || conclusion == "timed_out" || status == "failed" || status == "canceled" {
		return "Failed"
	}
	return "Pending"
}

func (s *store) providerJSON(ctx context.Context, endpoint, token, provider string, target any) error {
	if token == "" {
		return fmt.Errorf("%s token is not configured", provider)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ReleasePilot/0.2")
	if provider == "github" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	} else {
		req.Header.Set("PRIVATE-TOKEN", token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 800))
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%s credentials were rejected", provider)
		case http.StatusForbidden:
			return fmt.Errorf("%s token does not have the required read-only permissions", provider)
		case http.StatusNotFound:
			return fmt.Errorf("%s resource was not found or is not visible to this token", provider)
		default:
			return fmt.Errorf("%s API returned %s", provider, resp.Status)
		}
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

type aiReport struct {
	Decision       string   `json:"decision"`
	Health         int      `json:"health"`
	Summary        string   `json:"summary"`
	Recommendation string   `json:"recommendation"`
	Risks          []risk   `json:"risks"`
	RollbackPlan   []string `json:"rollbackPlan"`
	ReleaseNotes   []string `json:"releaseNotes"`
}

func (s *store) generateAIReport(ctx context.Context, settings appSettings, input analyzeRequest, ctxData releaseContext) (aiReport, error) {
	contextJSON, _ := json.Marshal(ctxData)
	schema := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"decision", "health", "summary", "recommendation", "risks", "rollbackPlan", "releaseNotes"},
		"properties": map[string]any{
			"decision":       map[string]any{"type": "string", "enum": []string{"GO", "NEEDS VALIDATION", "NO-GO"}},
			"health":         map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
			"summary":        map[string]any{"type": "string"},
			"recommendation": map[string]any{"type": "string"},
			"risks": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"title", "level", "impact", "evidence"},
				"properties": map[string]any{"title": map[string]any{"type": "string"}, "level": map[string]any{"type": "string", "enum": []string{"Critical", "High", "Medium", "Low"}}, "impact": map[string]any{"type": "string"}, "evidence": map[string]any{"type": "string"}},
			}},
			"rollbackPlan": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"releaseNotes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	payload := map[string]any{
		"model":        settings.OpenAIModel,
		"instructions": "You are ReleasePilot, a cautious release manager. Analyze only the supplied repository signals. Be concise, cite commit SHAs or changed file paths in evidence, and do not invent test or production results. Give useful engineering feedback for the dashboard.",
		"input":        fmt.Sprintf("Create a release readiness report for provider=%s repository=%s base=%s release=%s. Repository signals: %s", input.Provider, input.Repository, input.BaseBranch, input.ReleaseBranch, contextJSON),
		"text":         map[string]any{"format": map[string]any{"type": "json_schema", "name": "release_report", "strict": true, "schema": schema}},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(body))
	if err != nil {
		return aiReport{}, err
	}
	req.Header.Set("Authorization", "Bearer "+settings.OpenAIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return aiReport{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return aiReport{}, safeOpenAIError(resp)
	}
	var response struct {
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return aiReport{}, err
	}
	for _, output := range response.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" {
				var result aiReport
				if err := json.Unmarshal([]byte(content.Text), &result); err != nil {
					return aiReport{}, err
				}
				return result, nil
			}
		}
	}
	return aiReport{}, errors.New("OpenAI response did not contain output text")
}

func safeOpenAIError(resp *http.Response) error {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 2000)).Decode(&payload)
	switch payload.Error.Code {
	case "insufficient_quota":
		return errors.New("OpenAI quota is exhausted; check project billing and limits")
	case "invalid_api_key":
		return errors.New("OpenAI API key is invalid")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("OpenAI rate limit reached; retry shortly")
	}
	return fmt.Errorf("OpenAI request failed with %s", resp.Status)
}
