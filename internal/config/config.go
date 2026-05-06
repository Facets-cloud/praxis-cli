// Package config manages the per-user CLI configuration at ~/.praxis/config.json.
//
// Today config is just the Praxis deployment URL override. URL resolution
// order (highest priority first):
//
//  1. --url flag value (when non-empty)
//  2. PRAXIS_URL environment variable
//  3. url field in ~/.praxis/config.json (set by `praxis configure --url X`)
//  4. DefaultURL constant (https://askpraxis.ai)
//
// Most users never see step 3 — the default works.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// DefaultURL is the multi-tenant SaaS hub. Customers with their own
// deployment override via `praxis configure --url <their-url>` or
// PRAXIS_URL.
const DefaultURL = "https://askpraxis.ai"

// Source describes which level of the resolution chain produced a value.
// Surfaced via `praxis configure --show` so users (and AI hosts) can
// understand WHERE a URL came from.
type Source string

const (
	SourceFlag    Source = "flag"
	SourceEnv     Source = "env"
	SourceFile    Source = "file"
	SourceDefault Source = "default"
)

// Resolved is the result of looking up a value with provenance.
type Resolved struct {
	URL    string `json:"url"`
	Source Source `json:"source"`
}

// Stored is the on-disk schema. Kept narrow on purpose — token storage
// lives in a separate file (~/.praxis/credentials).
type Stored struct {
	URL string `json:"url,omitempty"`
}

// ResolveURL applies the precedence order and returns the effective URL
// plus where it came from.
func ResolveURL(flagURL string) (Resolved, error) {
	if flagURL != "" {
		return Resolved{URL: flagURL, Source: SourceFlag}, nil
	}
	if env := os.Getenv("PRAXIS_URL"); env != "" {
		return Resolved{URL: env, Source: SourceEnv}, nil
	}
	stored, err := Load()
	if err != nil {
		return Resolved{}, err
	}
	if stored.URL != "" {
		return Resolved{URL: stored.URL, Source: SourceFile}, nil
	}
	return Resolved{URL: DefaultURL, Source: SourceDefault}, nil
}

// Load reads ~/.praxis/config.json. Missing file is not an error.
func Load() (Stored, error) {
	path, err := paths.Config()
	if err != nil {
		return Stored{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Stored{}, nil
		}
		return Stored{}, fmt.Errorf("read %s: %w", path, err)
	}
	var s Stored
	if err := json.Unmarshal(data, &s); err != nil {
		return Stored{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// Save writes ~/.praxis/config.json atomically (temp + rename) so a
// crash mid-write doesn't corrupt the file.
func Save(s Stored) error {
	path, err := paths.Config()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
