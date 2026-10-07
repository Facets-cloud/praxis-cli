package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/clifeed"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
)

// reportEnv gives a test its own HOME, no CI markers, and stubbed network.
// It returns the requests sendReport received and the reports queued.
func reportEnv(t *testing.T, sendErr error) (*[]clifeed.ReportRequest, *[]clifeed.ReportRequest) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, v := range httpclient.CIEnvVars() {
		t.Setenv(v, "")
	}
	sent, queued := &[]clifeed.ReportRequest{}, &[]clifeed.ReportRequest{}
	origS, origQ := sendReport, queueReport
	t.Cleanup(func() { sendReport, queueReport, reportMessage, reportJSON = origS, origQ, "", false })
	sendReport = func(_ context.Context, req clifeed.ReportRequest) (clifeed.ReportResult, error) {
		*sent = append(*sent, req)
		if sendErr != nil {
			return clifeed.ReportResult{}, sendErr
		}
		return clifeed.ReportResult{Status: "filed", ID: "r-1"}, nil
	}
	queueReport = func(req clifeed.ReportRequest, cli string) error {
		if cli != "praxis" {
			t.Errorf("queued as %q", cli)
		}
		*queued = append(*queued, req)
		return nil
	}
	return sent, queued
}

func runReportCmd(t *testing.T, stdin string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	reportCmd.SetOut(&out)
	reportCmd.SetIn(strings.NewReader(stdin))
	err := reportCmd.RunE(reportCmd, nil)
	return out.String(), err
}

func TestReportFromFlagIsFiled(t *testing.T) {
	sent, queued := reportEnv(t, nil)
	reportMessage = "first line\nhow I recovered"
	out, err := runReportCmd(t, "")
	if err != nil || len(*sent) != 1 || len(*queued) != 0 {
		t.Fatalf("err=%v sent=%d queued=%d", err, len(*sent), len(*queued))
	}
	if (*sent)[0].Body != "first line\nhow I recovered" || (*sent)[0].Footer.Channel != "stable" {
		t.Errorf("request = %+v", (*sent)[0])
	}
	var res clifeed.ReportResult
	if json.Unmarshal([]byte(out), &res) != nil || res.Status != "filed" {
		t.Errorf("output = %q", out)
	}
	if strings.Contains(out, "http") {
		t.Errorf("output names a URL: %q", out)
	}
}

func TestReportFromStdin(t *testing.T) {
	sent, _ := reportEnv(t, nil)
	if _, err := runReportCmd(t, "  from stdin\n"); err != nil || len(*sent) != 1 || (*sent)[0].Body != "from stdin" {
		t.Fatalf("err=%v sent=%+v", err, *sent)
	}
}

func TestReportEmptyIsRefused(t *testing.T) {
	sent, _ := reportEnv(t, nil)
	if _, err := runReportCmd(t, "   "); err == nil || len(*sent) != 0 {
		t.Fatalf("err=%v sent=%d", err, len(*sent))
	}
}

func TestReportIsOffInCI(t *testing.T) {
	sent, queued := reportEnv(t, nil)
	t.Setenv("GITHUB_ACTIONS", "true")
	reportMessage = "x"
	_, err := runReportCmd(t, "")
	if err == nil || !strings.Contains(err.Error(), "off in CI") || len(*sent)+len(*queued) != 0 {
		t.Fatalf("err=%v sent=%d queued=%d", err, len(*sent), len(*queued))
	}
}

func TestReportQueuesOnlyTransientFailures(t *testing.T) {
	for _, c := range []struct {
		err   *clifeed.ReportError
		queue bool
	}{
		{&clifeed.ReportError{Err: errors.New("connection refused")}, true},
		{&clifeed.ReportError{Status: http.StatusBadGateway, Code: "upstream"}, true},
		{&clifeed.ReportError{Status: http.StatusTooManyRequests, Code: "rate_limited", RetryAfter: 60}, false},
		{&clifeed.ReportError{Status: http.StatusServiceUnavailable, Code: "reports_disabled"}, false},
		{&clifeed.ReportError{Status: http.StatusBadRequest, Code: "bad_body", Reason: "too long"}, false},
	} {
		_, queued := reportEnv(t, c.err)
		reportMessage = "x"
		out, err := runReportCmd(t, "")
		if c.queue {
			if err != nil || len(*queued) != 1 || !strings.Contains(out, "queued") {
				t.Errorf("%v: err=%v queued=%d out=%q", c.err, err, len(*queued), out)
			}
			continue
		}
		if err == nil || len(*queued) != 0 {
			t.Errorf("%v: err=%v queued=%d", c.err, err, len(*queued))
		}
	}
}

func TestRecordHistoryKeepsFlagNamesOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range httpclient.CIEnvVars() {
		t.Setenv(v, "")
	}
	recordHistory([]string{"mcp", "cloud_cli", "list", "--json", "--profile=s3cr3t-value", "-p", "other-s3cr3t"}, "mcp", time.Now(), true)
	recordHistory([]string{"--version"}, "", time.Now(), false)
	recordHistory([]string{"version"}, "version", time.Now(), false)
	recordHistory([]string{"hook", "x"}, "hook", time.Now(), false)

	raw, err := os.ReadFile(filepath.Join(home, ".facets", "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cr3t") || strings.Contains(string(raw), "cloud_cli") {
		t.Fatalf("history holds an argument or a value: %s", raw)
	}
	got := clifeed.ReadHistory(10)
	if len(got) != 1 || got[0].Command != "mcp" || got[0].Exit != 1 || got[0].CLI != "praxis" ||
		strings.Join(got[0].Flags, " ") != "--json --profile -p" {
		t.Fatalf("history = %+v", got)
	}
}

func TestRecordHistoryIsOffInCI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CI", "true")
	recordHistory([]string{"status"}, "status", time.Now(), false)
	if _, err := os.Stat(filepath.Join(home, ".facets", "history.jsonl")); err == nil {
		t.Fatal("history was written in CI")
	}
}

func TestFlushIsSkippedForReportAndInCI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range httpclient.CIEnvVars() {
		t.Setenv(v, "")
	}
	if err := clifeed.QueueReport(clifeed.ReportRequest{Body: "kept"}, "praxis"); err != nil {
		t.Fatal(err)
	}
	// TestMain points the feed at a closed port, so a flush that ran would
	// keep the file anyway; these calls must not even try, and must return fast.
	start := time.Now()
	flushQueuedReports([]string{"report", "-m", "x"}, "report")
	t.Setenv("CI", "true")
	flushQueuedReports([]string{"status"}, "status")
	if time.Since(start) > 2*time.Second || !clifeed.HasQueuedReports() {
		t.Error("flush ran where it is skipped")
	}
}

// A report over the server's limit is refused here, and is neither sent nor queued.
func TestReportOverTheServerLimitIsRefused(t *testing.T) {
	sent, queued := reportEnv(t, nil)
	reportMessage = ""
	_, err := runReportCmd(t, strings.Repeat("x", reportMaxBody+1))
	if err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("err = %v, want a size refusal", err)
	}
	if len(*sent) != 0 || len(*queued) != 0 {
		t.Errorf("sent=%d queued=%d, want none", len(*sent), len(*queued))
	}
}
