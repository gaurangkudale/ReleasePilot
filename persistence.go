package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type persistedState struct {
	Releases map[string]release `json:"releases"`
	Settings appSettings        `json:"settings"`
}

func (s *store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.Releases != nil {
		s.releases = make(map[string]release)
		for id, item := range state.Releases {
			if item.Provider == "demo" {
				continue
			}
			s.releases[id] = normalizeRelease(item)
		}
	}
	if state.Settings.OpenAIModel != "" {
		s.settings.OpenAIModel = state.Settings.OpenAIModel
	}
	if state.Settings.GitHub.Username != "" || state.Settings.GitHub.Token != "" {
		s.settings.GitHub = state.Settings.GitHub
	}
	if state.Settings.GitLab.Username != "" || state.Settings.GitLab.Token != "" {
		s.settings.GitLab = state.Settings.GitLab
	}
	if state.Settings.OpenAIKey != "" {
		s.settings.OpenAIKey = state.Settings.OpenAIKey
	}
	return nil
}

func (s *store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	state := persistedState{Releases: s.releases, Settings: s.settings}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}
