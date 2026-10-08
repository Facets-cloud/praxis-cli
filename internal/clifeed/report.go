package clifeed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// reportHistoryRows is how many history entries a report attaches.
const reportHistoryRows = 10

// reportQueueDir holds reports that could not be sent. raptor uses the same
// folder, and either CLI sends what the other queued.
const reportQueueDir = ".facets/reports"

// reportTimeout bounds one report request.
const reportTimeout = 10 * time.Second

// ReportFooter is what the CLI attaches to a report. The user cannot edit it.
type ReportFooter struct {
	Channel string         `json:"channel"`
	CPURL   string         `json:"cp_url,omitempty"`
	History []HistoryEntry `json:"history,omitempty"`
}

// ReportRequest is the body of POST /cli/v1/report.
type ReportRequest struct {
	InstallID string       `json:"install_id,omitempty"`
	Body      string       `json:"body"`
	Footer    ReportFooter `json:"footer"`
}

// ReportResult is the answer to a filed report. It never names where the
// report landed.
type ReportResult struct {
	Status string `json:"status"`
	ID     string `json:"id,omitempty"`
}

// ReportError is a report the feed did not file.
type ReportError struct {
	// Status is the HTTP status, or 0 when no response came back.
	Status     int
	Code       string
	Reason     string
	RetryAfter int
	Err        error
}

func (e *ReportError) Error() string {
	switch {
	case e.Status == 0:
		return fmt.Sprintf("the report could not reach the feed: %v", e.Err)
	case e.Code == "rate_limited" && e.RetryAfter > 0:
		return fmt.Sprintf("too many reports for now; try again in %d seconds", e.RetryAfter)
	case e.Code == "reports_disabled":
		return "friction reports are not turned on at the feed"
	case e.Code == "reports_off_in_ci":
		return "reports are off in CI: a pipeline would file the same report on every run"
	case e.Reason != "":
		return fmt.Sprintf("the report was refused (%s): %s", e.Code, e.Reason)
	case e.Code != "":
		return fmt.Sprintf("the report was refused (%s)", e.Code)
	}
	return fmt.Sprintf("the report was refused (HTTP %d)", e.Status)
}

// Transient reports whether a later attempt can succeed: no response came
// back, or the feed or GitHub failed. Such a report is queued; a refusal is not.
func (e *ReportError) Transient() bool {
	switch e.Status {
	case 0, http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// reportEndpoint is the report route beside the check route, so
// FACETS_CLI_FEED_URL moves both.
func reportEndpoint() string {
	base := strings.TrimSuffix(endpoint(), "/cli/v1/check")
	return strings.TrimSuffix(base, "/") + "/cli/v1/report"
}

// NewReport builds a report: the body, and the footer the CLI attaches. praxis
// follows the stable channel only.
func NewReport(body string) ReportRequest {
	req := ReportRequest{
		InstallID: InstallID(),
		Body:      body,
		Footer:    ReportFooter{Channel: "stable", History: ReadHistory(reportHistoryRows)},
	}
	if a, err := credentials.ResolveActive(""); err == nil && a.Loaded {
		req.Footer.CPURL = a.Profile.URL
	}
	return req
}

// SendReport files one report. An error is always a *ReportError.
func SendReport(ctx context.Context, req ReportRequest) (ReportResult, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return ReportResult{}, &ReportError{Err: err}
	}
	return sendRaw(ctx, raw)
}

func sendRaw(ctx context.Context, raw []byte) (ReportResult, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, reportEndpoint(), bytes.NewReader(raw))
	if err != nil {
		return ReportResult{}, &ReportError{Err: err}
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := httpclient.New(reportTimeout).Do(r)
	if err != nil {
		return ReportResult{}, &ReportError{Err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		var out ReportResult
		if err := json.Unmarshal(body, &out); err != nil || out.Status == "" {
			out.Status = "filed"
		}
		return out, nil
	}
	var e struct {
		Error             string `json:"error"`
		Reason            string `json:"reason"`
		RetryAfterSeconds int    `json:"retry_after_seconds"`
	}
	_ = json.Unmarshal(body, &e)
	return ReportResult{}, &ReportError{Status: resp.StatusCode, Code: e.Error, Reason: e.Reason, RetryAfter: e.RetryAfterSeconds}
}

func queueDir() (string, error) {
	home, err := paths.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, reportQueueDir), nil
}

// QueueReport keeps a report that could not be sent, for a later command to
// send.
func QueueReport(req ReportRequest, cli string) error {
	dir, err := queueDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return writeQueued(dir, fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), cli), raw)
}

// writeQueued writes a queue file through a temporary name and a rename, so a
// flush in another process never reads a half-written report.
func writeQueued(dir, name string, raw []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

// requeueAtBack gives a report that failed again a new, current name, so the
// oldest-first order tries the others first next time. The CLI suffix stays.
func requeueAtBack(dir, name string, raw []byte) {
	_, suffix, _ := strings.Cut(name, "-")
	if writeQueued(dir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), suffix), raw) == nil {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// FlushReports sends up to max queued reports, oldest first. A filed report,
// one refused for good (a 4xx other than 429) and one refused because reports
// are off (503) are removed. A transient failure moves the report to the back
// of the queue, so it cannot block the others, and stops the flush, because
// the next one would likely fail the same way. A 429 keeps the report and
// stops. It is silent and best-effort.
func FlushReports(ctx context.Context, max int) {
	dir, err := queueDir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i, name := range names {
		if i == max || ctx.Err() != nil {
			return
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !json.Valid(raw) {
			_ = os.Remove(path)
			continue
		}
		_, err = sendRaw(ctx, raw)
		var re *ReportError
		switch {
		case errors.As(err, &re) && re.Transient():
			requeueAtBack(dir, name, raw)
			return
		case errors.As(err, &re) && re.Status == http.StatusTooManyRequests:
			return
		}
		_ = os.Remove(path)
	}
}

// HasQueuedReports reports whether any report waits in the queue, so a caller
// can skip the flush cheaply.
func HasQueuedReports() bool {
	dir, err := queueDir()
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			return true
		}
	}
	return false
}
