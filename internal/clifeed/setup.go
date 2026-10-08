package clifeed

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// setup tells the feed how praxis is installed on this machine. It goes with
// the daily check only. Paths under the home folder start with "~". Unlike
// raptor, praxis sends no profile list: the check carries only the active
// profile.
type setup struct {
	InstallMethod string      `json:"install_method,omitempty"`
	BinaryPath    string      `json:"binary_path,omitempty"`
	PathCopies    []pathCopy  `json:"path_copies,omitempty"`
	Skills        []skillCopy `json:"skills,omitempty"`
	SkillsError   string      `json:"skills_error,omitempty"`
	LegacySkills  int         `json:"legacy_skills"`
	CatalogSkills int         `json:"catalog_skills"`
}

// raptorSkillErrorFile holds the error of the last failed raptor skill install
// (~/.praxis/raptor-skill-error). A successful install removes it.
const raptorSkillErrorFile = "raptor-skill-error"

// RecordRaptorSkillError keeps the error of a raptor skill install for the
// setup snapshot, or removes the last one when err is nil. Best-effort.
func RecordRaptorSkillError(err error) {
	dir, dErr := paths.Dir()
	if dErr != nil {
		return
	}
	p := filepath.Join(dir, raptorSkillErrorFile)
	if err == nil {
		_ = os.Remove(p)
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(p, []byte(err.Error()), 0o644)
}

func raptorSkillError() string {
	dir, err := paths.Dir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, raptorSkillErrorFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SkillsKey changes when the installed skills change, so the next command
// sends a new check with a fresh snapshot. A check that a skill command sends
// at its start would otherwise hold the skills from before that command.
func SkillsKey() string {
	home, _ := paths.Home()
	skills, legacy, catalog := skillState(home)
	versions := make([]string, 0, len(skills))
	for _, k := range skills {
		versions = append(versions, k.Host+"="+k.Version)
	}
	sort.Strings(versions)
	return fmt.Sprintf("%d/%d/%t/%s", legacy, catalog, raptorSkillError() != "", strings.Join(versions, ","))
}

type pathCopy struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

type skillCopy struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

// maxPathCopies matches the server's limit.
const maxPathCopies = 10

// copyVersionTimeout bounds `version` on another praxis copy, so a wedged copy
// cannot use up the feed's time.
var copyVersionTimeout = time.Second

var (
	jsonVersion = regexp.MustCompile(`"version"\s*:\s*"v?(\d+\.\d+\.\d+[^"]*)"`)
	textVersion = regexp.MustCompile(`praxis version v?(\d+\.\d+\.\d+\S*)`)
)

// collectSetup builds the snapshot. Every part is best-effort.
func collectSetup() *setup {
	home, _ := paths.Home()
	s := &setup{}
	self := resolvedSelf()
	if self != "" {
		s.InstallMethod = installMethod(self, home)
		s.BinaryPath = tilde(self, home)
	}
	s.PathCopies = pathCopies(os.Getenv("PATH"), self, home)
	s.Skills, s.LegacySkills, s.CatalogSkills = skillState(home)
	s.SkillsError = raptorSkillError()
	return s
}

func resolvedSelf() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// installMethod names how the binary at path (symlinks resolved) was installed.
func installMethod(path, home string) string {
	if _, ok := selfupdate.HomebrewCask(path); ok || strings.Contains(path, "/Cellar/") || strings.HasPrefix(path, "/opt/homebrew/") {
		return "brew"
	}
	switch dir := filepath.Dir(path); {
	case home != "" && dir == filepath.Join(home, ".local", "bin"):
		return "local-bin"
	case dir == "/usr/local/bin":
		return "usr-local-bin"
	case home != "" && dir == filepath.Join(home, "go", "bin"):
		return "go"
	}
	return "other"
}

// pathCopies lists every praxis on the PATH, in PATH order, with its version.
// The running binary reports its own version. Another copy runs `version`,
// which skips the first-run setup and the skill refresh, with the update check
// off. (`--version` would not skip them.)
func pathCopies(pathEnv, self, home string) []pathCopy {
	var out []pathCopy
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || len(out) == maxPathCopies {
			continue
		}
		candidate := filepath.Join(dir, "praxis")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil || seen[resolved] {
			continue
		}
		seen[resolved] = true
		c := pathCopy{Path: tilde(candidate, home)}
		if resolved == self {
			c.Version = strings.TrimPrefix(httpclient.Version, "v")
		} else {
			c.Version = copyVersion(resolved)
		}
		out = append(out, c)
	}
	return out
}

func copyVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), copyVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	cmd.Env = append(os.Environ(), "PRAXIS_NO_UPDATE_CHECK=1")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if m := jsonVersion.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	if m := textVersion.FindSubmatch(out); m != nil {
		return string(m[1])
	}
	return ""
}

// skillState reads the skill receipt: the embedded skills (with their content
// digest), the number of old praxis-* copies of skills that the consolidated
// skills replace, and the number of other catalog skills.
func skillState(home string) (skills []skillCopy, legacy, catalog int) {
	list, err := skillinstall.List()
	if err != nil {
		return nil, 0, 0
	}
	for _, in := range list {
		switch {
		case in.Source == "embedded":
			v := in.Digest
			if len(v) > 12 {
				v = v[:12]
			}
			skills = append(skills, skillCopy{Name: in.SkillName, Host: in.Harness, Path: tilde(filepath.Dir(in.Path), home), Version: v})
		case skillcatalog.ReplacedBy(in.SkillName) != "" && in.Scope != "organization" && in.Scope != "personal":
			// Only a GLOBAL copy is replaced. Old receipts have no scope.
			legacy++
		default:
			catalog++
		}
	}
	return skills, legacy, catalog
}

// tilde writes a path under home as "~/...", so the census holds no home
// folder names.
func tilde(path, home string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}
