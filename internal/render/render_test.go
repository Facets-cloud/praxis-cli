package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestIsTTY_BufferIsNotTTY(t *testing.T) {
	if IsTTY(&bytes.Buffer{}) {
		t.Error("bytes.Buffer should not be reported as TTY")
	}
}

func TestUseJSON_PrettyFlagWins(t *testing.T) {
	if UseJSON(true, true, &bytes.Buffer{}) {
		t.Error("--pretty should win over --json")
	}
}

func TestUseJSON_JSONFlagForces(t *testing.T) {
	if !UseJSON(true, false, &bytes.Buffer{}) {
		t.Error("--json should force JSON")
	}
}

func TestUseJSON_NonTTYAutoJSON(t *testing.T) {
	// bytes.Buffer is not TTY → auto JSON.
	if !UseJSON(false, false, &bytes.Buffer{}) {
		t.Error("non-TTY writer should auto-JSON")
	}
}

func TestJSON_Encoding(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, map[string]any{"hello": "world", "n": 42}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got["hello"] != "world" {
		t.Errorf("hello = %v, want world", got["hello"])
	}
	// Pretty-indented (contains newline + spaces).
	if !strings.Contains(buf.String(), "\n  ") {
		t.Errorf("output should be indented:\n%s", buf.String())
	}
}

func TestPrintError_JSON(t *testing.T) {
	var buf bytes.Buffer
	PrintError(&buf, true, "auth missing", "run `praxis login`", 3)
	var got Error
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("error JSON didn't parse: %v\noutput: %s", err, buf.String())
	}
	if got.Error != "auth missing" || got.Hint != "run `praxis login`" || got.Code != 3 {
		t.Errorf("got %+v", got)
	}
}

func TestPrintError_Text(t *testing.T) {
	var buf bytes.Buffer
	PrintError(&buf, false, "auth missing", "run `praxis login`", 3)
	out := buf.String()
	if !strings.Contains(out, "Error: auth missing") {
		t.Errorf("output missing 'Error:' line:\n%s", out)
	}
	if !strings.Contains(out, "Hint: run `praxis login`") {
		t.Errorf("output missing 'Hint:' line:\n%s", out)
	}
}

func TestPrintError_TextNoHint(t *testing.T) {
	var buf bytes.Buffer
	PrintError(&buf, false, "boom", "", 1)
	if strings.Contains(buf.String(), "Hint:") {
		t.Errorf("no hint should mean no Hint: line:\n%s", buf.String())
	}
}
