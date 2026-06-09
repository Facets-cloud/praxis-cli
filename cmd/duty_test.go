package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/duties"
)

// resetDutyFlags returns the module-level cobra-bound vars to defaults
// between tests — cobra holds these globals across RunE calls.
func resetDutyFlags() {
	dutyJSON = false
	dutyAgent = defaultDutyAgent
	dutyRunsDuty = ""
	dutyRunsLimit = 20
	dutyFindingsStatus = "open"
	dutyFindingsLimit = 200
}

// stubAgentResolution makes FetchIncludingGlobal return a single global
// "praxis" agent so resolveAgentID maps the default --agent to its id.
// Returns a restore func.
func stubAgentResolution(t *testing.T, praxisID string) func() {
	t.Helper()
	orig := agentcatalog.FetchIncludingGlobal
	agentcatalog.FetchIncludingGlobal = func(baseURL, token string) ([]agentcatalog.Agent, error) {
		return []agentcatalog.Agent{{ID: praxisID, Name: "praxis", Scope: "global", IsActive: true}}, nil
	}
	return func() { agentcatalog.FetchIncludingGlobal = orig }
}

// stubScheduleResolution makes ListSchedules map a duty name → id.
func stubScheduleResolution(t *testing.T, name, id string) func() {
	t.Helper()
	orig := duties.ListSchedules
	duties.ListSchedules = func(baseURL, token, agentID, tag string) ([]duties.Schedule, error) {
		return []duties.Schedule{{ID: id, AgentID: agentID, Name: name, DisplayName: "Prod Watch", Status: "active", Enabled: true}}, nil
	}
	return func() { duties.ListSchedules = orig }
}

func seedDutyProfile(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	if err := credentials.Put("default", credentials.Profile{
		URL: "https://x.test", Username: "u@x.com", Token: "sk_test_T",
	}); err != nil {
		t.Fatal(err)
	}
}

// --- list -------------------------------------------------------------

func TestDutyList_ResolvesAgentAndEmitsJSON(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()
	dutyJSON = true

	restoreAgent := stubAgentResolution(t, "agt_praxis")
	defer restoreAgent()

	orig := duties.ListSchedules
	duties.ListSchedules = func(baseURL, token, agentID, tag string) ([]duties.Schedule, error) {
		if baseURL != "https://x.test" || token != "sk_test_T" {
			t.Errorf("auth threading: url=%q token=%q", baseURL, token)
		}
		if agentID != "agt_praxis" {
			t.Errorf("agentID = %q; want agt_praxis (resolved from default --agent praxis)", agentID)
		}
		return []duties.Schedule{{ID: "sch1", Name: "prod-watch", DisplayName: "Prod Watch", Status: "active", Enabled: true, OpenFindingsCount: 2}}, nil
	}
	defer func() { duties.ListSchedules = orig }()

	var buf bytes.Buffer
	dutyListCmd.SetOut(&buf)
	dutyListCmd.SetErr(&buf)
	if err := dutyListCmd.RunE(dutyListCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(parsed) != 1 || parsed[0]["name"] != "prod-watch" {
		t.Errorf("parsed = %+v", parsed)
	}
}

func TestDutyList_EmptyEmitsArray(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()
	dutyJSON = true
	defer stubAgentResolution(t, "agt_praxis")()

	orig := duties.ListSchedules
	duties.ListSchedules = func(baseURL, token, agentID, tag string) ([]duties.Schedule, error) {
		return nil, nil
	}
	defer func() { duties.ListSchedules = orig }()

	var buf bytes.Buffer
	dutyListCmd.SetOut(&buf)
	if err := dutyListCmd.RunE(dutyListCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not JSON (must be [] not null): %v\n%s", err, buf.String())
	}
	if len(parsed) != 0 {
		t.Errorf("want empty array, got %+v", parsed)
	}
}

// --- runs (with --duty name resolution) -------------------------------

func TestDutyRuns_ResolvesDutyNameToScheduleID(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()
	dutyJSON = true
	dutyRunsDuty = "prod-watch"
	dutyRunsLimit = 5

	defer stubAgentResolution(t, "agt_praxis")()
	defer stubScheduleResolution(t, "prod-watch", "sch1")()

	orig := duties.ListRuns
	duties.ListRuns = func(baseURL, token, agentID, scheduleID string, limit int) ([]duties.Run, error) {
		if agentID != "agt_praxis" || scheduleID != "sch1" {
			t.Errorf("resolution wrong: agent=%q schedule=%q", agentID, scheduleID)
		}
		if limit != 5 {
			t.Errorf("limit = %d; want 5", limit)
		}
		art := "art1"
		return []duties.Run{{ID: "run1", Status: "completed", ScheduleID: scheduleID, ReportArtifactID: &art}}, nil
	}
	defer func() { duties.ListRuns = orig }()

	var buf bytes.Buffer
	dutyRunsCmd.SetOut(&buf)
	if err := dutyRunsCmd.RunE(dutyRunsCmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(parsed) != 1 || parsed[0]["id"] != "run1" {
		t.Errorf("parsed = %+v", parsed)
	}
}

// --- run --------------------------------------------------------------

func TestDutyRun_EmitsRunDetail(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()
	dutyJSON = true
	defer stubAgentResolution(t, "agt_praxis")()

	orig := duties.GetRun
	duties.GetRun = func(baseURL, token, agentID, runID string) (*duties.Run, error) {
		if runID != "run9" {
			t.Errorf("runID = %q", runID)
		}
		art := "art9"
		return &duties.Run{ID: "run9", Status: "completed", ReportArtifactID: &art}, nil
	}
	defer func() { duties.GetRun = orig }()

	var buf bytes.Buffer
	dutyRunCmd.SetOut(&buf)
	if err := dutyRunCmd.RunE(dutyRunCmd, []string{"run9"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if parsed["report_artifact_id"] != "art9" {
		t.Errorf("report_artifact_id = %v", parsed["report_artifact_id"])
	}
}

// --- report (two-hop: run detail → artifact content) ------------------

func TestDutyReport_FetchesArtifactContent(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()
	dutyJSON = true
	defer stubAgentResolution(t, "agt_praxis")()

	origRun := duties.GetRun
	duties.GetRun = func(baseURL, token, agentID, runID string) (*duties.Run, error) {
		art := "art9"
		return &duties.Run{ID: runID, ReportArtifactID: &art}, nil
	}
	defer func() { duties.GetRun = origRun }()

	origArt := duties.FetchArtifactContent
	duties.FetchArtifactContent = func(baseURL, token, artifactID string) ([]byte, string, error) {
		if artifactID != "art9" {
			t.Errorf("artifactID = %q; want art9 (from run.report_artifact_id)", artifactID)
		}
		return []byte("# Nightly Report\nAll clear."), "text/markdown", nil
	}
	defer func() { duties.FetchArtifactContent = origArt }()

	var buf bytes.Buffer
	dutyReportCmd.SetOut(&buf)
	if err := dutyReportCmd.RunE(dutyReportCmd, []string{"run9"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if parsed["artifact_id"] != "art9" || parsed["mime"] != "text/markdown" {
		t.Errorf("envelope = %+v", parsed)
	}
	if parsed["content"] == "" {
		t.Error("content empty")
	}
}

// --- findings ---------------------------------------------------------

func TestDutyFindings_EmitsJSON(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()
	dutyJSON = true
	defer stubAgentResolution(t, "agt_praxis")()
	defer stubScheduleResolution(t, "prod-watch", "sch1")()

	orig := duties.ListFindings
	duties.ListFindings = func(baseURL, token, agentID, scheduleID, status string, limit int) ([]duties.Finding, error) {
		if scheduleID != "sch1" || status != "open" {
			t.Errorf("schedule=%q status=%q", scheduleID, status)
		}
		fk := "fk1"
		return []duties.Finding{{Title: "open ports", Severity: "critical", Status: "open", FindingKey: &fk, RecurrenceCount: 1}}, nil
	}
	defer func() { duties.ListFindings = orig }()

	var buf bytes.Buffer
	dutyFindingsCmd.SetOut(&buf)
	if err := dutyFindingsCmd.RunE(dutyFindingsCmd, []string{"prod-watch"}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	var parsed []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if len(parsed) != 1 || parsed[0]["finding_key"] != "fk1" {
		t.Errorf("parsed = %+v", parsed)
	}
}

// --- pretty printers --------------------------------------------------

func TestPrettyPrinters(t *testing.T) {
	last := "2026-06-01T00:00:00Z"
	svc := "api"
	env := "prod"
	fk := "fk1"
	art := "art1"

	var buf bytes.Buffer
	printSchedulesPretty(&buf, "alerts-auditor", "agt_x", []duties.Schedule{{
		ID: "sch1", Name: "prod-watch", DisplayName: "Prod Watch", Status: "active",
		Enabled: false, OpenFindingsCount: 3, CronExpression: "0 * * * *", Timezone: "UTC", LastRunAt: &last,
	}})
	if got := buf.String(); !contains(got, "prod-watch") || !contains(got, "disabled") || !contains(got, "0 * * * *") {
		t.Errorf("schedules pretty = %q", got)
	}
	buf.Reset()
	// Empty list must name the agent that was queried (and its resolved id).
	printSchedulesPretty(&buf, "praxis", "agt_praxis", nil)
	if got := buf.String(); !contains(got, "no duties") || !contains(got, `"praxis"`) || !contains(got, "agt_praxis") {
		t.Errorf("empty schedules must name the agent, got = %q", got)
	}

	buf.Reset()
	printRunsPretty(&buf, "alerts-auditor", "agt_x", "prod-watch", []duties.Run{{ID: "run1", Status: "completed", StartedAt: "t", ReportArtifactID: &art}})
	if got := buf.String(); !contains(got, "run1") || !contains(got, "report: yes") {
		t.Errorf("runs pretty = %q", got)
	}
	buf.Reset()
	// Empty runs with a --duty filter names both duty and agent (with resolved id).
	printRunsPretty(&buf, "alerts-auditor", "agt_x", "prod-watch", nil)
	if got := buf.String(); !contains(got, "no runs") || !contains(got, `"prod-watch"`) || !contains(got, `"alerts-auditor"`) || !contains(got, "agt_x") {
		t.Errorf("empty runs must name duty+agent (with resolved id), got = %q", got)
	}
	buf.Reset()
	// Empty runs without a filter names just the agent (id == arg → no bracket).
	printRunsPretty(&buf, "praxis", "praxis", "", nil)
	if got := buf.String(); !contains(got, "no runs") || !contains(got, `"praxis"`) {
		t.Errorf("empty runs must name agent, got = %q", got)
	}

	buf.Reset()
	printRunPretty(&buf, &duties.Run{
		ID: "run1", Status: "completed", ScheduleID: "sch1", StartedAt: "t", CompletedAt: &last,
		ReportArtifactID: &art, Findings: []duties.Finding{{Title: "disk full", Severity: "high"}},
	})
	if got := buf.String(); !contains(got, "report artifact: art1") || !contains(got, "disk full") {
		t.Errorf("run pretty = %q", got)
	}

	buf.Reset()
	printFindingsPretty(&buf, []duties.Finding{{
		Title: "open ports", Severity: "critical", Status: "open", FindingKey: &fk,
		RecurrenceCount: 2, Service: &svc, Environment: &env, Description: "ssh open to world",
	}})
	if got := buf.String(); !contains(got, "open ports") || !contains(got, "fk1") || !contains(got, "service: api") {
		t.Errorf("findings pretty = %q", got)
	}
	buf.Reset()
	printFindingsPretty(&buf, nil)
	if !contains(buf.String(), "no findings") {
		t.Errorf("empty findings = %q", buf.String())
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }

// --- resolution helpers ----------------------------------------------

func TestResolveAgentID_NameIDAndPassthrough(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()

	orig := agentcatalog.FetchIncludingGlobal
	agentcatalog.FetchIncludingGlobal = func(baseURL, token string) ([]agentcatalog.Agent, error) {
		return []agentcatalog.Agent{
			{ID: "agt_praxis", Name: "praxis", IsActive: true},
			{ID: "agt_org", Name: "org-bot", IsActive: true},
		}, nil
	}
	defer func() { agentcatalog.FetchIncludingGlobal = orig }()

	active, err := credentials.ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer

	if got := resolveAgentID(&buf, active, "praxis"); got != "agt_praxis" {
		t.Errorf("by-name: got %q; want agt_praxis", got)
	}
	if got := resolveAgentID(&buf, active, "agt_org"); got != "agt_org" {
		t.Errorf("by-id: got %q; want agt_org", got)
	}
	if got := resolveAgentID(&buf, active, "agt_unknown_raw"); got != "agt_unknown_raw" {
		t.Errorf("passthrough: got %q; want raw id back", got)
	}
}

func TestResolveScheduleID_NameIDAndPassthrough(t *testing.T) {
	seedDutyProfile(t)
	resetDutyFlags()
	defer resetDutyFlags()

	orig := duties.ListSchedules
	duties.ListSchedules = func(baseURL, token, agentID, tag string) ([]duties.Schedule, error) {
		return []duties.Schedule{
			{ID: "sch1", Name: "prod-watch"},
			{ID: "sch2", Name: "cost-audit"},
		}, nil
	}
	defer func() { duties.ListSchedules = orig }()

	active, err := credentials.ResolveActive("")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer

	if got := resolveScheduleID(&buf, active, "agt", "cost-audit"); got != "sch2" {
		t.Errorf("by-name: got %q; want sch2", got)
	}
	if got := resolveScheduleID(&buf, active, "agt", "sch1"); got != "sch1" {
		t.Errorf("by-id: got %q; want sch1", got)
	}
	if got := resolveScheduleID(&buf, active, "agt", "sch_raw"); got != "sch_raw" {
		t.Errorf("passthrough: got %q; want raw id back", got)
	}
}
