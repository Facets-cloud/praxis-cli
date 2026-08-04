package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
)

func resetProfilesManageFlags(t *testing.T) {
	t.Helper()
	profilesRenameJSON, profilesRmJSON = false, false
	t.Cleanup(func() { profilesRenameJSON, profilesRmJSON = false, false })
}

func TestProfilesRename_HappyPath(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "test-x", "https://cp.test", "tok")
	if err := credentials.SetActive("test-x"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	profilesRenameCmd.SetOut(&buf)
	profilesRenameJSON = true
	if err := profilesRenameCmd.RunE(profilesRenameCmd, []string{"test-x", "astuto-cp"}); err != nil {
		t.Fatalf("rename err: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"ok": true`, `"renamed_to": "astuto-cp"`, `"active_pointer_updated": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("rename output missing %q\nfull: %s", want, out)
		}
	}
	store, _ := credentials.Load()
	if _, ok := store["astuto-cp"]; !ok {
		t.Error("renamed profile missing from store")
	}
	active, _ := credentials.ResolveActiveGlobal()
	if active.Name != "astuto-cp" {
		t.Errorf("active = %s, want astuto-cp", active.Name)
	}
}

func TestProfilesRename_MissingOldExitsUsage(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	exit := stubOsExit(t)

	var buf bytes.Buffer
	profilesRenameCmd.SetOut(&buf)
	profilesRenameJSON = true
	if err := profilesRenameCmd.RunE(profilesRenameCmd, []string{"ghost", "new"}); err == nil {
		t.Fatal("rename of missing profile succeeded")
	}
	if *exit != exitcode.Usage {
		t.Errorf("osExit code = %d, want %d (Usage)", *exit, exitcode.Usage)
	}
}

func TestProfilesRm_NonActive(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	seedProfile(t, "stale", "https://old.test", "tok2")
	if err := credentials.SetActive("default"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"stale"}); err != nil {
		t.Fatalf("rm err: %v", err)
	}
	if !strings.Contains(buf.String(), `"removed": "stale"`) {
		t.Errorf("rm output missing removed marker: %s", buf.String())
	}
	store, _ := credentials.Load()
	if _, ok := store["stale"]; ok {
		t.Error("profile still in store after rm")
	}
	if _, ok := store["default"]; !ok {
		t.Error("unrelated profile vanished")
	}
}

func TestProfilesRm_ActiveIsRefused(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	exit := stubOsExit(t)

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"default"}); err == nil {
		t.Fatal("rm of active profile succeeded")
	}
	if *exit != exitcode.Usage {
		t.Errorf("osExit code = %d, want %d (Usage)", *exit, exitcode.Usage)
	}
	store, _ := credentials.Load()
	if _, ok := store["default"]; !ok {
		t.Error("active profile was deleted despite refusal")
	}
}

func TestProfilesRm_MissingExitsUsage(t *testing.T) {
	isolateHome(t)
	resetProfilesManageFlags(t)
	seedProfile(t, "default", "https://cp.test", "tok")
	exit := stubOsExit(t)

	var buf bytes.Buffer
	profilesRmCmd.SetOut(&buf)
	profilesRmJSON = true
	if err := profilesRmCmd.RunE(profilesRmCmd, []string{"ghost"}); err == nil {
		t.Fatal("rm of missing profile succeeded")
	}
	if *exit != exitcode.Usage {
		t.Errorf("osExit code = %d, want %d (Usage)", *exit, exitcode.Usage)
	}
}
