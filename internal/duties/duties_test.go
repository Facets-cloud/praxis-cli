package duties

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// assertReq pins the request's method, path, and Bearer token. Shared by
// stubServer and the query-asserting tests below so none of them lose the
// method/path/auth checks.
func assertReq(t *testing.T, r *http.Request, wantMethod, wantPath, wantBearer string) {
	t.Helper()
	if r.Method != wantMethod {
		t.Errorf("method = %s; want %s", r.Method, wantMethod)
	}
	if r.URL.Path != wantPath {
		t.Errorf("path = %s; want %s", r.URL.Path, wantPath)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+wantBearer {
		t.Errorf("auth header = %q; want %q", got, "Bearer "+wantBearer)
	}
}

// stubServer spins up an httptest.Server that asserts the request method,
// path, and Bearer header, then returns the table-row's status + body.
// Optionally streams a Content-Type so the artifact-content path can be
// exercised. Mirrors internal/memory's stubServer.
//
// For tests that also need to assert query params, build the server with
// queryStubServer instead — reassigning srv.Config.Handler would silently
// drop these method/path/auth checks.
func stubServer(t *testing.T, wantMethod, wantPath string, status int, body, contentType, wantBearer string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, wantMethod, wantPath, wantBearer)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// queryStubServer is stubServer plus a per-request hook to assert query
// params, all in one handler so the method/path/auth assertions still run.
func queryStubServer(t *testing.T, wantMethod, wantPath, body, wantBearer string, checkQuery func(*testing.T, *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertReq(t, r, wantMethod, wantPath, wantBearer)
		checkQuery(t, r)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- ListSchedules -----------------------------------------------------

func TestListSchedules_HappyPath(t *testing.T) {
	const body = `[
		{"id":"sch1","agent_id":"agt","name":"prod-watch","display_name":"Prod Watch","cron_expression":"0 * * * *","timezone":"UTC","enabled":true,"objective":"watch","status":"active","consecutive_errors":0,"created_by_email":"u@x","created_at":"t","updated_at":"t","open_findings_count":3,"learnings_count":1,"tags":["prod"]},
		{"id":"sch2","agent_id":"agt","name":"cost-audit","display_name":"Cost Audit","cron_expression":"0 0 * * *","timezone":"UTC","enabled":false,"objective":"audit","status":"paused","consecutive_errors":2,"created_by_email":"u@x","created_at":"t","updated_at":"t","open_findings_count":0,"learnings_count":0,"tags":[]}
	]`
	srv := stubServer(t, http.MethodGet, "/ai-api/custom-agents/agt/schedules", 200, body, "", "tok")
	got, err := ListSchedules(srv.URL, "tok", "agt", "")
	if err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].Name != "prod-watch" || got[0].OpenFindingsCount != 3 {
		t.Errorf("first = %+v", got[0])
	}
}

func TestListSchedules_TagFilterEncoded(t *testing.T) {
	hit := false
	srv := queryStubServer(t, http.MethodGet, "/ai-api/custom-agents/agt/schedules", "[]", "tok",
		func(t *testing.T, r *http.Request) {
			hit = true
			if r.URL.Query().Get("tag") != "prod" {
				t.Errorf("tag = %q; want prod", r.URL.Query().Get("tag"))
			}
		})
	if _, err := ListSchedules(srv.URL, "tok", "agt", "prod"); err != nil {
		t.Fatalf("ListSchedules: %v", err)
	}
	if !hit {
		t.Error("handler not hit")
	}
}

func TestListSchedules_RequiresAgentID(t *testing.T) {
	_, err := ListSchedules("http://no-such-host.invalid", "tok", "", "")
	if err == nil || !strings.Contains(err.Error(), "agentID is required") {
		t.Fatalf("err = %v; want agentID required", err)
	}
}

// --- ListRuns ----------------------------------------------------------

func TestListRuns_HappyPathWithScheduleAndLimit(t *testing.T) {
	const body = `[{"id":"run1","agent_id":"agt","schedule_id":"sch1","organization_id":"o","status":"completed","started_at":"t","report_artifact_id":"art1","findings":[],"actions":[]}]`
	srv := queryStubServer(t, http.MethodGet, "/ai-api/custom-agents/agt/runs", body, "tok",
		func(t *testing.T, r *http.Request) {
			if r.URL.Query().Get("schedule_id") != "sch1" {
				t.Errorf("schedule_id = %q", r.URL.Query().Get("schedule_id"))
			}
			if r.URL.Query().Get("limit") != "5" {
				t.Errorf("limit = %q; want 5", r.URL.Query().Get("limit"))
			}
		})
	got, err := ListRuns(srv.URL, "tok", "agt", "sch1", 5)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(got) != 1 || got[0].ReportArtifactID == nil || *got[0].ReportArtifactID != "art1" {
		t.Fatalf("got = %+v", got)
	}
}

// --- GetRun ------------------------------------------------------------

func TestGetRun_CarriesReportArtifactID(t *testing.T) {
	const body = `{"id":"run9","agent_id":"agt","schedule_id":"sch1","organization_id":"o","status":"completed","started_at":"t","report_artifact_id":"art9","findings":[{"title":"disk full","severity":"high","description":"d","finding_key":"k1","recurrence_count":2,"status":"open"}],"actions":[]}`
	srv := stubServer(t, http.MethodGet, "/ai-api/custom-agents/agt/runs/run9", 200, body, "", "tok")
	got, err := GetRun(srv.URL, "tok", "agt", "run9")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ReportArtifactID == nil || *got.ReportArtifactID != "art9" {
		t.Errorf("report_artifact_id = %v", got.ReportArtifactID)
	}
	if len(got.Findings) != 1 || got.Findings[0].Severity != "high" {
		t.Errorf("findings = %+v", got.Findings)
	}
}

// --- ListFindings (envelope unwrap) ------------------------------------

func TestListFindings_UnwrapsItemsEnvelope(t *testing.T) {
	const body = `{"items":[
		{"title":"open ports","severity":"critical","description":"d","finding_key":"fk1","recurrence_count":1,"status":"open"},
		{"title":"stale cert","severity":"medium","description":"d","finding_key":"fk2","recurrence_count":4,"status":"open"}
	]}`
	srv := queryStubServer(t, http.MethodGet, "/ai-api/custom-agents/agt/schedules/sch1/findings", body, "tok",
		func(t *testing.T, r *http.Request) {
			if r.URL.Query().Get("status") != "open" {
				t.Errorf("status = %q; want open", r.URL.Query().Get("status"))
			}
		})
	got, err := ListFindings(srv.URL, "tok", "agt", "sch1", "open", 0)
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d; want 2", len(got))
	}
	if got[0].FindingKey == nil || *got[0].FindingKey != "fk1" {
		t.Errorf("first finding_key = %v", got[0].FindingKey)
	}
}

// --- FetchArtifactContent (raw body + mime) ----------------------------

func TestFetchArtifactContent_ReturnsBodyAndMime(t *testing.T) {
	const report = "# Nightly Report\n\nAll clear."
	srv := stubServer(t, http.MethodGet, "/ai-api/artifacts/art9/content", 200, report, "text/markdown; charset=utf-8", "tok")
	body, mime, err := FetchArtifactContent(srv.URL, "tok", "art9")
	if err != nil {
		t.Fatalf("FetchArtifactContent: %v", err)
	}
	if string(body) != report {
		t.Errorf("body = %q", string(body))
	}
	if !strings.HasPrefix(mime, "text/markdown") {
		t.Errorf("mime = %q; want text/markdown...", mime)
	}
}

func TestFetchArtifactContent_404SurfacesError(t *testing.T) {
	srv := stubServer(t, http.MethodGet, "/ai-api/artifacts/gone/content", 404, "not found", "", "tok")
	_, _, err := FetchArtifactContent(srv.URL, "tok", "gone")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v; want HTTP 404", err)
	}
}

func TestFetchArtifactContent_RequiresArtifactID(t *testing.T) {
	_, _, err := FetchArtifactContent("http://x.test", "tok", "")
	if err == nil || !strings.Contains(err.Error(), "artifactID is required") {
		t.Fatalf("err = %v; want artifactID required", err)
	}
}

// --- transport edge: token/baseURL required ----------------------------

func TestDoJSON_RequiresBaseURLAndToken(t *testing.T) {
	if _, err := ListRuns("", "tok", "agt", "", 0); err == nil {
		t.Error("want error for empty baseURL")
	}
	if _, err := ListRuns("http://x.test", "", "agt", "", 0); err == nil {
		t.Error("want error for empty token")
	}
}
