// Package memory is the REST client for the Praxis memories API
// (/ai-api/memories on the deployment). It mirrors the layout of
// internal/skillcatalog: types match the server's MemoryResponse /
// MemoryCreate / MemoryRecallRequest shapes, exported function vars
// give tests a seam to swap, and every transport call uses
// Authorization: Bearer <token>.
//
// Praxis-cli's memory commands invoke memories at audience=user|org
// scope. agent_id is intentionally omitted on POST — the server's
// audience_to_cell() routes the row into the correct (org, user, None)
// cell. This is what the POST /memories ?agent_id= was relaxed to
// accept in the backend slice that landed alongside this package.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// basePath is the router prefix for memories on the deployment.
	basePath = "/ai-api/memories"

	defaultTimeout = 30 * time.Second
)

// Kind mirrors the server's MemoryKind enum (purpose taxonomy:
// user | feedback | project | reference). Kept as a string so callers
// can pass through values from --kind without an enum table here.
type Kind string

// Audience mirrors the server's MemoryAudience enum (agent | user | org).
// Praxis-cli writes default to "user"; audience="org" lifts the row out
// of the per-user cell so every user in the org can recall it.
type Audience string

const (
	AudienceAgent Audience = "agent"
	AudienceUser  Audience = "user"
	AudienceOrg   Audience = "org"
)

// Importance mirrors the server's MemoryImportance enum
// (low | medium | high | critical). Default at the server is medium.
type Importance string

// Memory is the wire shape of MemoryResponse. Field names track the
// server's JSON tags. Only the fields the CLI surfaces today are
// retained; new fields can be added without breaking older binaries
// because encoding/json ignores unknown keys.
type Memory struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	UserID         *string    `json:"user_id,omitempty"`
	AgentID        *string    `json:"agent_id,omitempty"`
	Title          string     `json:"title"`
	Slug           string     `json:"slug"`
	Content        string     `json:"content"`
	Summary        *string    `json:"summary,omitempty"`
	IndexHook      *string    `json:"index_hook,omitempty"`
	Kind           Kind       `json:"kind"`
	Audience       Audience   `json:"audience"`
	Category       string     `json:"category"`
	Importance     Importance `json:"importance"`
	Tags           []string   `json:"tags"`
	RelevanceScore *float64   `json:"relevance_score,omitempty"`
}

// CreateRequest is the wire shape of MemoryCreate.
type CreateRequest struct {
	Title      string     `json:"title"`
	Slug       string     `json:"slug,omitempty"`
	Content    string     `json:"content"`
	Summary    string     `json:"summary,omitempty"`
	IndexHook  string     `json:"index_hook,omitempty"`
	Kind       Kind       `json:"kind,omitempty"`
	Audience   Audience   `json:"audience,omitempty"`
	Importance Importance `json:"importance,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
}

// RecallRequest is the wire shape of MemoryRecallRequest. Query is
// required; Limit defaults to 5 server-side (we still send it for
// clarity).
type RecallRequest struct {
	Query         string     `json:"query"`
	Limit         int        `json:"limit"`
	Category      string     `json:"category,omitempty"`
	MinImportance Importance `json:"min_importance,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
}

// ListParams filters the GET /memories call. Empty fields are omitted
// from the query string.
type ListParams struct {
	Category   string
	Importance Importance
	Tags       []string
	Limit      int
	Offset     int
}

// HTTP seams — tests swap these to avoid the network.

// Recall posts a RecallRequest and returns the scored matches.
var Recall = func(baseURL, token string, req RecallRequest) ([]Memory, error) {
	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return doJSON[[]Memory](baseURL, token, http.MethodPost, basePath+"/recall", bytes.NewReader(body))
}

// List fetches memories filtered by ListParams.
var List = func(baseURL, token string, p ListParams) ([]Memory, error) {
	q := url.Values{}
	if p.Category != "" {
		q.Set("category", p.Category)
	}
	if p.Importance != "" {
		q.Set("importance", string(p.Importance))
	}
	if len(p.Tags) > 0 {
		q.Set("tags", strings.Join(p.Tags, ","))
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Offset > 0 {
		q.Set("offset", strconv.Itoa(p.Offset))
	}
	path := basePath
	if encoded := q.Encode(); encoded != "" {
		path = path + "?" + encoded
	}
	return doJSON[[]Memory](baseURL, token, http.MethodGet, path, nil)
}

// Create posts a CreateRequest WITHOUT ?agent_id= (audience-driven cell
// placement on the server). Returns the persisted Memory.
var Create = func(baseURL, token string, req CreateRequest) (*Memory, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if req.Content == "" {
		return nil, fmt.Errorf("content is required")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	m, err := doJSON[Memory](baseURL, token, http.MethodPost, basePath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// doJSON is the shared transport. Returns a typed payload or an error
// shaped as `HTTP <status> from <url>: <body-prefix>` so callers can
// branch on status without re-parsing the URL.
//
// Uses http.NewRequestWithContext with a bounded timeout so cancellation
// propagates (noctx lint expectation). The http.Client{Timeout} on top
// is belt-and-braces — it also bounds connection + handshake time
// before the context deadline kicks in.
func doJSON[T any](baseURL, token, method, path string, body io.Reader) (T, error) {
	var zero T
	if baseURL == "" {
		return zero, fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return zero, fmt.Errorf("token is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	full := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf(
			"HTTP %d from %s: %s",
			resp.StatusCode, full, truncate(string(raw), 200),
		)
	}

	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, fmt.Errorf("parse response: %w", err)
	}
	return out, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
