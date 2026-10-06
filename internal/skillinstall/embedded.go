package skillinstall

import (
	"embed"
	"io/fs"
)

// treeSkillFiles holds every embedded skill as a real file tree: SKILL.md plus
// references, scripts and assets.
//
//go:embed embedded/praxis
var treeSkillFiles embed.FS

// treeSkillNames is the set of embedded skills, in embed order.
var treeSkillNames = []string{praxisSkillName}

// treeSkills maps a binary-embedded multi-file skill name to its rooted file
// tree (SKILL.md at the root, plus subdirectories like flows/). Tree skills
// install via InstallTree instead of the single-file ContentFor path, and
// they are meta-skills (preserved on profile switch — see IsMetaSkill).
func treeSkills() map[string]fs.FS {
	out := make(map[string]fs.FS, len(treeSkillNames))
	for _, name := range treeSkillNames {
		// The embed paths are compile-time constants, so fs.Sub cannot fail
		// in a correctly-built binary.
		sub, err := fs.Sub(treeSkillFiles, "embedded/"+name)
		if err != nil {
			panic("skillinstall: embedded tree skill missing: " + name + ": " + err.Error())
		}
		out[name] = sub
	}
	return out
}

// isTreeSkill reports whether `name` is a binary-embedded multi-file skill.
func isTreeSkill(name string) bool {
	_, ok := treeSkills()[name]
	return ok
}

// treeSkillFS returns the embedded file tree for a tree skill, if any.
func treeSkillFS(name string) (fs.FS, bool) {
	fsys, ok := treeSkills()[name]
	return fsys, ok
}
