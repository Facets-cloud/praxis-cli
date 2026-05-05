package cmd

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestVersionCmd_PrintsAllFields(t *testing.T) {
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	versionCmd.Run(versionCmd, nil)

	out := buf.String()
	wantSubstrings := []string{
		"praxis version " + version,
		"commit:",
		commit,
		"built:",
		date,
		"go:",
		runtime.Version(),
		"os/arch:",
		runtime.GOOS + "/" + runtime.GOARCH,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q\nfull output:\n%s", want, out)
		}
	}
}
