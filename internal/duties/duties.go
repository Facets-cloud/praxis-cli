// Package duties is the read-only REST client for Praxis Agent
// Schedules ("duties") on a deployment. Duties run unattended on a cron
// and emit findings (deduped recurring issues) and artifacts (reports);
// this package lets the CLI — and the AI host driving it — query those
// runs and read the artifact a run produced.
//
// It mirrors the layout of internal/memory: typed structs track the
// server's response models (AgentScheduleResponse / AgentRunResponse /
// Finding), exported function vars give tests a seam to swap, and every
// transport call sends Authorization: Bearer <token>.
//
// Schedules are nested under a custom agent. The CLI resolves the agent
// id (the global "praxis" duty agent by default) via internal/agentcatalog
// before calling here — agentID is always passed in, never assumed.
//
// Backend routes (all GET, under /ai-api):
//
//	GET /custom-agents/{agent}/schedules
//	GET /custom-agents/{agent}/runs?schedule_id=&limit=
//	GET /custom-agents/{agent}/runs/{run}
//	GET /custom-agents/{agent}/schedules/{schedule}/findings?status=&limit=
//	GET /artifacts/{artifact}/content
package duties

import (
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
	// apiPrefix is the deployment's AI-API mount. Agent/artifact routers
	// live under it (mirrors internal/memory's /ai-api/memories).
	apiPrefix = "/ai-api"

	defaultTimeout = 30 * time.Second
)

// Schedule is the wire shape of AgentScheduleResponse. Only the fields
// the CLI surfaces are retained — encoding/json ignores the rest, so
// older binaries keep working as the server adds fields.
type Schedule struct {
	ID                string   `json:"id"`
	AgentID           string   `json:"agent_id"`
	OrganizationID    string   `json:"organization_id"`
	Name              string   `json:"name"`
	DisplayName       string   `json:"display_name"`
	CronExpression    string   `json:"cron_expression"`
	Timezone          string   `json:"timezone"`
	Enabled           bool     `json:"enabled"`
	Objective         string   `json:"objective"`
	Status            string   `json:"status"`
	ConsecutiveErrors int      `json:"consecutive_errors"`
	LastRunAt         *string  `json:"last_run_at,omitempty"`
	CreatedByEmail    string   `json:"created_by_email"`
	CreatedAt         string   `json:"created_at"`
	UpdatedAt         string   `json:"updated_at"`
	OpenFindingsCount int      `json:"open_findings_count"`
	LearningsCount    int      `json:"learnings_count"`
	Tags              []string `json:"tags"`
}

// Finding is the wire shape of the server's Finding sub-model. FindingKey
// is the stable identity slug used to dedupe a recurring issue across
// runs and to address it on resolve.
type Finding struct {
	Title           string  `json:"title"`
	Severity        string  `json:"severity"`
	Description     string  `json:"description"`
	Environment     *string `json:"environment,omitempty"`
	Service         *string `json:"service,omitempty"`
	ArtifactID      *string `json:"artifact_id,omitempty"`
	FindingKey      *string `json:"finding_key,omitempty"`
	FirstSeenRunID  *string `json:"first_seen_run_id,omitempty"`
	LastSeenRunID   *string `json:"last_seen_run_id,omitempty"`
	RecurrenceCount int     `json:"recurrence_count"`
	Status          string  `json:"status"`
	ResolvedAt      *string `json:"resolved_at,omitempty"`
	LastSeenAt      *string `json:"last_seen_at,omitempty"`
}

// Action is the wire shape of the server's Action sub-model — what a run
// did (opened a PR, filed an issue, …).
type Action struct {
	Type   string  `json:"type"`
	Title  string  `json:"title"`
	URL    *string `json:"url,omitempty"`
	Reason string  `json:"reason"`
}

// Run is the wire shape of AgentRunResponse. ReportArtifactID points at
// the report a run produced — `duty report` resolves it then fetches the
// artifact body.
type Run struct {
	ID               string    `json:"id"`
	AgentID          string    `json:"agent_id"`
	ScheduleID       string    `json:"schedule_id"`
	OrganizationID   string    `json:"organization_id"`
	Status           string    `json:"status"`
	StartedAt        string    `json:"started_at"`
	CompletedAt      *string   `json:"completed_at,omitempty"`
	Findings         []Finding `json:"findings"`
	Actions          []Action  `json:"actions"`
	Cost             *float64  `json:"cost,omitempty"`
	TokensUsed       *int      `json:"tokens_used,omitempty"`
	DurationMs       *int      `json:"duration_ms,omitempty"`
	ErrorMessage     *string   `json:"error_message,omitempty"`
	SessionID        *string   `json:"session_id,omitempty"`
	ReportArtifactID *string   `json:"report_artifact_id,omitempty"`
}

// findingsEnvelope unwraps the `{"items": [...]}` shape the findings
// endpoint returns (vs the bare arrays the other reads return).
type findingsEnvelope struct {
	Items []Finding `json:"items"`
}

// --- HTTP seams — tests swap these to avoid the network. ---------------

// ListSchedules returns every duty under an agent, optionally filtered by
// tag.
var ListSchedules = func(baseURL, token, agentID, tag string) ([]Schedule, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentID is required")
	}
	path := agentBase(agentID) + "/schedules"
	if tag != "" {
		q := url.Values{}
		q.Set("tag", tag)
		path += "?" + q.Encode()
	}
	return doJSON[[]Schedule](baseURL, token, http.MethodGet, path, nil)
}

// ListRuns returns runs under an agent, newest first. A non-empty
// scheduleID filters to one duty; limit is clamped server-side to 1-100.
var ListRuns = func(baseURL, token, agentID, scheduleID string, limit int) ([]Run, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentID is required")
	}
	q := url.Values{}
	if scheduleID != "" {
		q.Set("schedule_id", scheduleID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := agentBase(agentID) + "/runs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return doJSON[[]Run](baseURL, token, http.MethodGet, path, nil)
}

// GetRun returns a single run's detail, including report_artifact_id.
var GetRun = func(baseURL, token, agentID, runID string) (*Run, error) {
	if agentID == "" || runID == "" {
		return nil, fmt.Errorf("agentID and runID are required")
	}
	path := agentBase(agentID) + "/runs/" + url.PathEscape(runID)
	run, err := doJSON[Run](baseURL, token, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// ListFindings returns a duty's findings deduped by finding_key. status is
// one of open|resolved|all; limit is clamped server-side to 1-1000.
var ListFindings = func(baseURL, token, agentID, scheduleID, status string, limit int) ([]Finding, error) {
	if agentID == "" || scheduleID == "" {
		return nil, fmt.Errorf("agentID and scheduleID are required")
	}
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := agentBase(agentID) + "/schedules/" + url.PathEscape(scheduleID) + "/findings"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	env, err := doJSON[findingsEnvelope](baseURL, token, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return env.Items, nil
}

// FetchArtifactContent returns an artifact's raw body and its MIME type.
// The /content endpoint streams bytes (text/markdown or text/html), not
// JSON, so this bypasses doJSON and reads the body + Content-Type directly.
var FetchArtifactContent = func(baseURL, token, artifactID string) (body []byte, mime string, err error) {
	if baseURL == "" {
		return nil, "", fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return nil, "", fmt.Errorf("token is required")
	}
	if artifactID == "" {
		return nil, "", fmt.Errorf("artifactID is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	full := strings.TrimRight(baseURL, "/") + apiPrefix + "/artifacts/" + url.PathEscape(artifactID) + "/content"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, full, truncate(string(raw), 200))
	}
	return raw, resp.Header.Get("Content-Type"), nil
}

// --- transport ---------------------------------------------------------

// agentBase builds the per-agent route prefix shared by the schedule,
// run, and finding endpoints.
func agentBase(agentID string) string {
	return apiPrefix + "/custom-agents/" + url.PathEscape(agentID)
}

// doJSON is the shared transport. Returns a typed payload or an error
// shaped as `HTTP <status> from <url>: <body-prefix>` so the cmd layer
// can branch on status (401/403 → auth) without re-parsing the URL.
// Copied deliberately from internal/memory to keep the two clients'
// error contracts identical.
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
