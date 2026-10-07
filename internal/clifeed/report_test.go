package clifeed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHistoryAppendReadAndTrim(t *testing.T) {
	home := isolate(t)
	for i := 0; i < 12; i++ {
		if err := AppendHistory(HistoryEntry{CLI: "praxis", Command: "c" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	got := ReadHistory(10)
	if len(got) != 10 || got[0].Command != "c2" || got[9].Command != "c11" {
		t.Fatalf("ReadHistory = %+v", got)
	}
	path := filepath.Join(home, historyFile)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("history file: %v, %v", info, err)
	}

	// Past the size limit, only the newer half stays, at a line boundary.
	big := strings.Repeat("x", 4096)
	for i := 0; i < 300; i++ {
		_ = AppendHistory(HistoryEntry{CLI: "praxis", Command: big + strconv.Itoa(i)})
	}
	info, _ := os.Stat(path)
	if info.Size() > historyMaxBytes {
		t.Errorf("history is %d bytes, over the limit", info.Size())
	}
	last := ReadHistory(1)
	if len(last) != 1 || !strings.HasSuffix(last[0].Command, "299") {
		t.Errorf("newest entry lost after trim: %+v", last)
	}
}

func TestNewReportAttachesTheFooter(t *testing.T) {
	isolate(t)
	for i := 0; i < 15; i++ {
		_ = AppendHistory(HistoryEntry{CLI: "raptor", Command: "get projects", Flags: []string{"-o"}})
	}
	req := NewReport("first line\nmore")
	if req.Footer.Channel != "stable" || req.Footer.CPURL != "https://acme.console.facets.cloud" {
		t.Errorf("footer = %+v", req.Footer)
	}
	if len(req.Footer.History) != reportHistoryRows {
		t.Errorf("history rows = %d, want %d", len(req.Footer.History), reportHistoryRows)
	}
	if !installIDPattern.MatchString(req.InstallID) {
		t.Errorf("install_id = %q", req.InstallID)
	}
}

func TestReportEndpointFollowsTheFeedURL(t *testing.T) {
	t.Setenv("FACETS_CLI_FEED_URL", "https://x.test/cli/v1/check")
	if got := reportEndpoint(); got != "https://x.test/cli/v1/report" {
		t.Errorf("reportEndpoint = %q", got)
	}
	t.Setenv("FACETS_CLI_FEED_URL", "http://127.0.0.1:9/")
	if got := reportEndpoint(); got != "http://127.0.0.1:9/cli/v1/report" {
		t.Errorf("reportEndpoint = %q", got)
	}
}

// reportServer answers every report with status and body, and records the
// request bodies.
func reportServer(t *testing.T, status int, body string, got *[]ReportRequest) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/v1/report" || r.Header.Get("X-Facets-Client") == "" {
			t.Errorf("request %s %q without the identity header", r.URL.Path, r.Header.Get("X-Facets-Client"))
		}
		raw, _ := io.ReadAll(r.Body)
		var req ReportRequest
		_ = json.Unmarshal(raw, &req)
		if got != nil {
			*got = append(*got, req)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FACETS_CLI_FEED_URL", srv.URL+"/cli/v1/check")
}

func TestSendReportOutcomes(t *testing.T) {
	isolate(t)
	cases := []struct {
		status    int
		body      string
		transient bool
		code      string
	}{
		{http.StatusBadRequest, `{"error":"bad_body","reason":"body is required"}`, false, "bad_body"},
		{http.StatusForbidden, `{"error":"reports_off_in_ci"}`, false, "reports_off_in_ci"},
		{http.StatusTooManyRequests, `{"error":"rate_limited","retry_after_seconds":120}`, false, "rate_limited"},
		{http.StatusServiceUnavailable, `{"error":"reports_disabled"}`, false, "reports_disabled"},
		{http.StatusBadGateway, `{"error":"upstream"}`, true, "upstream"},
		{http.StatusInternalServerError, ``, true, ""},
	}
	for _, c := range cases {
		reportServer(t, c.status, c.body, nil)
		_, err := SendReport(context.Background(), NewReport("x"))
		var re *ReportError
		if !errors.As(err, &re) || re.Transient() != c.transient || re.Code != c.code {
			t.Errorf("HTTP %d: err = %v (%+v)", c.status, err, re)
		}
	}
	reportServer(t, http.StatusTooManyRequests, `{"error":"rate_limited","retry_after_seconds":120}`, nil)
	if _, err := SendReport(context.Background(), NewReport("x")); err == nil || !strings.Contains(err.Error(), "120 seconds") {
		t.Errorf("rate limit message = %v", err)
	}

	var got []ReportRequest
	reportServer(t, http.StatusCreated, `{"status":"filed","id":"r-1"}`, &got)
	res, err := SendReport(context.Background(), NewReport("first line"))
	if err != nil || res.Status != "filed" || res.ID != "r-1" || len(got) != 1 || got[0].Body != "first line" {
		t.Fatalf("filed = %+v, %v, %+v", res, err, got)
	}
}

func TestSendReportWithoutAnAnswerIsTransient(t *testing.T) {
	isolate(t)
	t.Setenv("FACETS_CLI_FEED_URL", "http://127.0.0.1:1/cli/v1/check")
	_, err := SendReport(context.Background(), NewReport("x"))
	var re *ReportError
	if !errors.As(err, &re) || !re.Transient() || re.Status != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestQueueAndFlush(t *testing.T) {
	home := isolate(t)
	for i := 0; i < 2; i++ {
		if err := QueueReport(NewReport("queued "+strconv.Itoa(i)), "praxis"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	dir := filepath.Join(home, reportQueueDir)
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("queue dir: %v, %v", info, err)
	}
	if !HasQueuedReports() {
		t.Fatal("HasQueuedReports = false")
	}

	// A transient failure keeps every report, and moves the one it tried to the
	// back of the queue.
	reportServer(t, http.StatusBadGateway, `{"error":"upstream"}`, nil)
	FlushReports(context.Background(), 3)
	if n := countFiles(t, dir); n != 2 {
		t.Fatalf("after 502: %d files, want 2", n)
	}

	// Filed reports are removed, oldest first, up to the limit. "queued 0" went to
	// the back after the 502, so "queued 1" goes first.
	var got []ReportRequest
	reportServer(t, http.StatusCreated, `{"status":"filed","id":"r"}`, &got)
	FlushReports(context.Background(), 1)
	if n := countFiles(t, dir); n != 1 || len(got) != 1 || got[0].Body != "queued 1" {
		t.Fatalf("after one flush: %d files, sent %+v", n, got)
	}

	// A refusal is removed too: it would never succeed.
	reportServer(t, http.StatusBadRequest, `{"error":"bad_body"}`, nil)
	FlushReports(context.Background(), 3)
	if HasQueuedReports() {
		t.Error("a refused report stayed in the queue")
	}
}

func TestFlushSendsRaptorQueuedReportsToo(t *testing.T) {
	home := isolate(t)
	dir := filepath.Join(home, reportQueueDir)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, "1-raptor.json"), []byte(`{"body":"from raptor","footer":{"channel":"unstable"}}`), 0o600)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"filed"}`))
	}))
	defer srv.Close()
	t.Setenv("FACETS_CLI_FEED_URL", srv.URL)
	FlushReports(context.Background(), 3)
	if calls.Load() != 1 || HasQueuedReports() {
		t.Errorf("calls = %d, queued = %t", calls.Load(), HasQueuedReports())
	}
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// A transient failure moves the report to the back and stops; a 429 keeps it in
// place and stops; a 503 removes it.
func TestFlushRotatesKeepsAndDrops(t *testing.T) {
	home := isolate(t)
	dir := filepath.Join(home, ".facets", "reports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(name, body string) {
		raw, _ := json.Marshal(ReportRequest{Body: body})
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("100-praxis.json", "transient")
	put("101-raptor.json", "disabled")
	put("102-praxis.json", "limited")
	status := map[string]int{"transient": http.StatusBadGateway, "disabled": http.StatusServiceUnavailable, "limited": http.StatusTooManyRequests}
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ReportRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		seen = append(seen, req.Body)
		w.WriteHeader(status[req.Body])
	}))
	defer srv.Close()
	t.Setenv("FACETS_CLI_FEED_URL", srv.URL+"/cli/v1/check")

	FlushReports(context.Background(), 3)
	if strings.Join(seen, ",") != "transient" {
		t.Fatalf("first flush sent %v, want only the transient one", seen)
	}
	FlushReports(context.Background(), 3)
	if strings.Join(seen, ",") != "transient,disabled,limited" {
		t.Fatalf("second flush sent %v", seen)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	// The 503 report is gone; the 429 report and the rotated transient one stay.
	if len(names) != 2 || names[0] != "102-praxis.json" || !strings.HasSuffix(names[1], "-praxis.json") || names[1] == "100-praxis.json" {
		t.Fatalf("queue = %v", names)
	}
}
