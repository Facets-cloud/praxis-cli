package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_URL", "")
	return home
}

func TestDefaultURL_PinnedToAskpraxis(t *testing.T) {
	if DefaultURL != "https://askpraxis.ai" {
		t.Fatalf("DefaultURL = %q; the published default URL must remain https://askpraxis.ai", DefaultURL)
	}
}

func TestResolveURL_Default(t *testing.T) {
	withHome(t)
	got, err := ResolveURL("")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != DefaultURL || got.Source != SourceDefault {
		t.Errorf("got %+v, want URL=%q Source=default", got, DefaultURL)
	}
}

func TestResolveURL_Flag_TopsAll(t *testing.T) {
	withHome(t)
	t.Setenv("PRAXIS_URL", "https://from-env.example.com")
	if err := Save(Stored{URL: "https://from-file.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveURL("https://from-flag.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://from-flag.example.com" || got.Source != SourceFlag {
		t.Errorf("flag should win, got %+v", got)
	}
}

func TestResolveURL_Env_BeatsFile(t *testing.T) {
	withHome(t)
	t.Setenv("PRAXIS_URL", "https://from-env.example.com")
	if err := Save(Stored{URL: "https://from-file.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveURL("")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://from-env.example.com" || got.Source != SourceEnv {
		t.Errorf("env should beat file, got %+v", got)
	}
}

func TestResolveURL_File_BeatsDefault(t *testing.T) {
	withHome(t)
	if err := Save(Stored{URL: "https://from-file.example.com"}); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveURL("")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://from-file.example.com" || got.Source != SourceFile {
		t.Errorf("file should beat default, got %+v", got)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	withHome(t)
	want := Stored{URL: "https://round-trip.example.com"}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestLoad_MissingFile_NoError(t *testing.T) {
	withHome(t)
	got, err := Load()
	if err != nil {
		t.Errorf("Load on fresh home should not error, got %v", err)
	}
	if got.URL != "" {
		t.Errorf("Load on fresh home returned non-empty %+v", got)
	}
}

func TestLoad_CorruptFile_Errors(t *testing.T) {
	home := withHome(t)
	cfgDir := filepath.Join(home, ".praxis")
	_ = os.MkdirAll(cfgDir, 0700)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte("{ not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Errorf("Load of corrupt JSON should error")
	}
}

func TestSave_ChmodTo0600(t *testing.T) {
	withHome(t)
	if err := Save(Stored{URL: "https://x.com"}); err != nil {
		t.Fatal(err)
	}
	// Read the actual on-disk file mode.
	home, _ := os.UserHomeDir()
	info, err := os.Stat(filepath.Join(home, ".praxis", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("config file perm = %o, want 0600", mode)
	}
}
