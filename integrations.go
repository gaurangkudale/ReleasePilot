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
		return "OpenAI is configured with model " + settings.OpenAIModel, nil
	default:
		return "", errors.New("unknown provider")
	}
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 800))
		return fmt.Errorf("%s API returned %s: %s", provider, resp.Status, strings.TrimSpace(string(body)))
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

func (s *store) generateAIReport(ctx context.Context, settings appSettings, input analyzeRequest, history []commit) (aiReport, error) {
	historyJSON, _ := json.Marshal(history)
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
		"instructions": "You are ReleasePilot, a cautious release manager. Analyze only the supplied repository history. Be concise, cite commit SHAs in evidence, and do not invent test or production results.",
		"input":        fmt.Sprintf("Create a release readiness report for provider=%s repository=%s base=%s release=%s. Recent commits: %s", input.Provider, input.Repository, input.BaseBranch, input.ReleaseBranch, historyJSON),
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
