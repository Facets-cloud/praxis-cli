package skillinstall

import (
	"fmt"
	"io/fs"
	"sort"
)

// praxisSkillName is the one skill embedded in this binary: the consolidated
// praxis package under embedded/praxis. Org catalog skills come from the
// server's /v1/skills/bundle endpoint and install beside it as praxis-<name>.
const praxisSkillName = "praxis"

// legacyBuiltinSkills are the embedded skills the praxis package replaced.
// Setup, login and refresh retire them on each host that has praxis.
var legacyBuiltinSkills = []string{"praxis-getting-started", "praxis-memory", "praxis-onboarding", "use-ig"}

// ContentFor returns the SKILL.md of an embedded skill. Org catalog skills
// come from the server and are not resolvable here.
func ContentFor(name string) (string, error) {
	tree, ok := treeSkillFS(name)
	if !ok {
		return "", fmt.Errorf("unknown skill %q (only embedded skills are resolvable; org skills come from the server)", name)
	}
	body, err := fs.ReadFile(tree, "SKILL.md")
	return string(body), err
}

// IsMetaSkill reports whether name is an embedded skill. Profile switches and
// logout keep embedded skills and wipe only org catalog skills.
func IsMetaSkill(name string) bool {
	return isTreeSkill(name)
}

// MetaSkillNames returns every embedded skill name, sorted.
func MetaSkillNames() []string {
	names := append([]string(nil), treeSkillNames...)
	sort.Strings(names)
	return names
}

// BootstrapSkillNames returns the embedded skills that install without a
// login. The praxis package covers sign-up and login itself, so that is all
// of them.
func BootstrapSkillNames() []string {
	return MetaSkillNames()
}
