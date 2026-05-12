package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/memory"
)

// resetMemoryFlags returns module-level cobra-bound vars to their
// defaults between tests. Cobra holds onto these globals — without
// reset, the previous test's flags leak into the next.
func resetMemoryFlags() {
	memoryJSON = true
	memoryRecallLimit = 5
	memoryListLimit = 100
	memoryListOffset = 0
	memoryListCategory = ""
	memoryListTagsCSV = ""
	memoryAddTitle = ""
	memoryAddContent = ""
	memoryAddSummary = ""
	memoryAddSlug = ""
	memoryAddKind = "user"
	memoryAddAudience = "user"
	memoryAddImportance = "medium"
	memoryAddTagsCSV = ""
}

// seedDefaultProfile wires HOME to a tempdir and writes a fake profile
// so activeOrAuthExit() resolves without trying to hit the real
// ~/.praxis.
func seedDefaultProfile(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	if err := credentials.Put("default", credentials.Profile{
		URL:      "https://x.test",
		Username: "u@x.com",
		Token:    "sk_test_T",
	}); err != nil {
		t.Fatal(err)
	}
}

// The CLI is AI-only — every command emits JSON unconditionally
// regardless of TTY. Tests assert on the JSON shape exclusively.

// ---------------------------------------------------------------------
// Recall
// ---------------------------------------------------------------------

func TestMemoryRecall_HappyPath_JSON(t *testing.T) {
	seedDefaultProfile(t)
	resetMemoryFlags()
	defer resetMemoryFlags()

	score := 1.42
	orig := memory.Recall
	memory.Recall = func(baseURL, token string, req memory.RecallRequest) ([]memory.Memory, error) {
		if baseURL != "https://x.test" || token != "sk_test_T" {
			t.Errorf("auth threading wrong: url=%q token=%q", baseURL, token)
		}
		if req.Query != "retry handling" {
			t.Errorf("query = %q", req.Query)
		}
		return []memory.Memory{{
			ID: "m1", Slug: "retry-budgets", Title: "Retry budgets",
			Content: "every external call wraps a 3-attempt backoff",
			Kind:    memory.Kind("feedback"), Audience: memory.AudienceUser,
			Category: "fact", Importance: memory.Importance("medium"),
			RelevanceScore: &score,
		}}, nil
	}
	defer func() { memory.Recall = orig }()

	var buf bytes.Buffer
	memoryRecallCmd.SetOut(&buf)
	memoryRecallCmd.SetErr(&buf)
	if err := memoryRecallCmd.RunE(memoryRecallCmd, []string{"retry", "handling"}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	if len(parsed) != 1 {
		t.Fatalf("got %d rows; want 1", len(parsed))
	}
	row := parsed[0]
	if row["slug"] != "retry-budgets" || row["title"] != "Retry budgets" {
		t.Errorf("row = %+v", row)
	}
	if score, ok := row["relevance_score"].(float64); !ok || score != 1.42 {
		t.Errorf("relevance_score = %v (%T)", row["relevance_score"], row["relevance_score"])
	}
}

func TestMemoryRecall_NoResults_EmitsEmptyArray(t *testing.T) {
	// Server returns nil; CLI must emit `[]` so the AI's JSON parser
	// always gets the same shape (no null vs array branching).
	seedDefaultProfile(t)
	resetMemoryFlags()
	defer resetMemoryFlags()

	orig := memory.Recall
	memory.Recall = func(string, string, memory.RecallRequest) ([]memory.Memory, error) {
		return nil, nil
	}
	defer func() { memory.Recall = orig }()

	var buf bytes.Buffer
	memoryRecallCmd.SetOut(&buf)
	if err := memoryRecallCmd.RunE(memoryRecallCmd, []string{"obscure"}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("expected `[]` for no results; got %q", got)
	}
}

// ---------------------------------------------------------------------
// List — full-dump-for-grep semantics
// ---------------------------------------------------------------------

func TestMemoryList_AppliesFilters(t *testing.T) {
	seedDefaultProfile(t)
	resetMemoryFlags()
	defer resetMemoryFlags()

	var captured memory.ListParams
	orig := memory.List
	memory.List = func(_, _ string, p memory.ListParams) ([]memory.Memory, error) {
		captured = p
		return []memory.Memory{
			{ID: "m1", Slug: "x", Title: "X", Content: "full content body",
				Kind: "user", Audience: memory.AudienceUser, Category: "fact",
				Importance: "medium"},
		}, nil
	}
	defer func() { memory.List = orig }()

	memoryListCategory = "fact"
	memoryListTagsCSV = "infra, ops"
	memoryListLimit = 25
	memoryListOffset = 5

	var buf bytes.Buffer
	memoryListCmd.SetOut(&buf)
	if err := memoryListCmd.RunE(memoryListCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	if captured.Category != "fact" {
		t.Errorf("category not forwarded: %+v", captured)
	}
	if len(captured.Tags) != 2 || captured.Tags[0] != "infra" || captured.Tags[1] != "ops" {
		t.Errorf("tags = %v", captured.Tags)
	}
	if captured.Limit != 25 || captured.Offset != 5 {
		t.Errorf("limit/offset = %d/%d", captured.Limit, captured.Offset)
	}

	// Output must be JSON and must include the full content (the
	// "list = full dump for grep" contract).
	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	if len(parsed) != 1 || parsed[0]["content"] != "full content body" {
		t.Errorf("expected full content in list output; got %+v", parsed)
	}
}

func TestMemoryList_EmptyResults_EmitsEmptyArray(t *testing.T) {
	seedDefaultProfile(t)
	resetMemoryFlags()
	defer resetMemoryFlags()

	orig := memory.List
	memory.List = func(string, string, memory.ListParams) ([]memory.Memory, error) {
		return nil, nil
	}
	defer func() { memory.List = orig }()

	var buf bytes.Buffer
	memoryListCmd.SetOut(&buf)
	if err := memoryListCmd.RunE(memoryListCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("expected `[]` for no results; got %q", got)
	}
}

// ---------------------------------------------------------------------
// Add
// ---------------------------------------------------------------------

func TestMemoryAdd_HappyPath_JSON(t *testing.T) {
	seedDefaultProfile(t)
	resetMemoryFlags()
	defer resetMemoryFlags()

	var captured memory.CreateRequest
	orig := memory.Create
	memory.Create = func(_, _ string, req memory.CreateRequest) (*memory.Memory, error) {
		captured = req
		return &memory.Memory{
			ID: "m1", Slug: "retry-budgets", Title: req.Title, Content: req.Content,
			Kind: req.Kind, Audience: req.Audience, Category: "fact",
			Importance: req.Importance, Tags: req.Tags,
		}, nil
	}
	defer func() { memory.Create = orig }()

	memoryAddTitle = "Retry budgets"
	memoryAddContent = "every external call wraps a 3-attempt backoff"
	memoryAddAudience = "org"
	memoryAddKind = "feedback"
	memoryAddImportance = "high"
	memoryAddTagsCSV = "infra,ops"

	var buf bytes.Buffer
	memoryAddCmd.SetOut(&buf)
	if err := memoryAddCmd.RunE(memoryAddCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if captured.Title != "Retry budgets" || captured.Audience != memory.AudienceOrg {
		t.Errorf("forwarded request wrong: %+v", captured)
	}
	if len(captured.Tags) != 2 || captured.Tags[0] != "infra" || captured.Tags[1] != "ops" {
		t.Errorf("tags = %v", captured.Tags)
	}
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	if parsed["slug"] != "retry-budgets" {
		t.Errorf("parsed slug = %v", parsed["slug"])
	}
}

func TestMemoryAdd_StdinContent(t *testing.T) {
	seedDefaultProfile(t)
	resetMemoryFlags()
	defer resetMemoryFlags()

	var captured memory.CreateRequest
	orig := memory.Create
	memory.Create = func(_, _ string, req memory.CreateRequest) (*memory.Memory, error) {
		captured = req
		return &memory.Memory{Title: req.Title, Slug: "s", Content: req.Content,
			Kind: req.Kind, Audience: req.Audience, Category: "fact",
			Importance: req.Importance}, nil
	}
	defer func() { memory.Create = orig }()

	memoryAddTitle = "From stdin"
	memoryAddContent = "-"

	stdin := bytes.NewBufferString("content piped from stdin")
	memoryAddCmd.SetIn(stdin)
	var buf bytes.Buffer
	memoryAddCmd.SetOut(&buf)
	if err := memoryAddCmd.RunE(memoryAddCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if captured.Content != "content piped from stdin" {
		t.Errorf("content not read from stdin: %q", captured.Content)
	}
}

// ---------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"  a , b , c  ", []string{"a", "b", "c"}},
		{",a,,b,", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("in=%q", tt.in), func(t *testing.T) {
			got := splitCSV(tt.in)
			if len(got) != len(tt.want) {
				t.Errorf("len(got)=%d want=%d (%v vs %v)", len(got), len(tt.want), got, tt.want)
				return
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("got[%d]=%q want=%q", i, v, tt.want[i])
				}
			}
		})
	}
}
