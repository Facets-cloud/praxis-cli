package clifeed

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// historyFile is the command history that a friction report attaches. raptor
// writes the same file, so a report shows the recent commands of both CLIs.
const historyFile = ".facets/history.jsonl"

// historyMaxBytes is the size past which the file keeps only its newer half.
const historyMaxBytes = 1 << 20

// HistoryEntry is one CLI invocation. It holds the command path and flag
// NAMES only: an argument or a flag value can hold a secret, so neither is
// ever written.
type HistoryEntry struct {
	Time       time.Time `json:"time"`
	CLI        string    `json:"cli"`
	Version    string    `json:"version"`
	Session    string    `json:"session,omitempty"`
	Command    string    `json:"command"`
	Flags      []string  `json:"flags,omitempty"`
	Exit       int       `json:"exit"`
	DurationMS int64     `json:"duration_ms"`
}

func historyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, historyFile), nil
}

// FlagNames returns the flag names in args, without values: "--output=json"
// gives "--output", and the value after "-p" is dropped because it does not
// start with "-".
func FlagNames(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			continue
		}
		name, _, _ := strings.Cut(a, "=")
		out = append(out, name)
	}
	return out
}

// AppendHistory adds one entry to the shared history file and keeps the file
// small. It is best-effort: a caller ignores the error, so history never fails
// a command.
func AppendHistory(e HistoryEntry) error {
	path, err := historyPath()
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	if info, err := os.Stat(path); err == nil && info.Size() > historyMaxBytes {
		return trimHistory(path)
	}
	return nil
}

// trimHistory keeps the newer half of the file, at a line boundary.
func trimHistory(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cut := bytes.IndexByte(raw[len(raw)/2:], '\n')
	if cut < 0 {
		return nil
	}
	return os.WriteFile(path, raw[len(raw)/2+cut+1:], 0o600)
}

// ReadHistory returns the last n entries, oldest first. A line that does not
// parse is skipped.
func ReadHistory(n int) []HistoryEntry {
	path, err := historyPath()
	if err != nil {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []HistoryEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e HistoryEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}
