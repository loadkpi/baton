package syncbus

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// builtinPrefix is the start of the names of the skills built into Baton. It
// is reserved: a skill of the source repository may not use it, so a built-in
// skill can never replace or be replaced by one of the user's.
const builtinPrefix = "baton-"

//go:embed builtin
var builtinFS embed.FS

// builtinSkills are the skills that ship inside the binary. Sync installs them
// into every Profile, whatever its tags and whatever the source holds, through
// the same manifest as any other skill.
func builtinSkills() ([]Skill, error) {
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skill := Skill{Name: e.Name()}
		root := "builtin/" + e.Name()
		err := fs.WalkDir(builtinFS, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := builtinFS.ReadFile(p)
			if err != nil {
				return err
			}
			skill.Files = append(skill.Files, File{Path: strings.TrimPrefix(p, root+"/"), Mode: 0o644, Data: data})
			return nil
		})
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

// withBuiltinSkills returns the source's skills and the built-in ones. A source
// skill whose name starts with the reserved prefix is an error that names it.
func withBuiltinSkills(skills []Skill) ([]Skill, error) {
	for _, s := range skills {
		if strings.HasPrefix(s.Name, builtinPrefix) {
			return nil, fmt.Errorf("skills/%s: names starting with %q are reserved for the skills built into Baton; rename it", s.Name, builtinPrefix)
		}
	}
	builtin, err := builtinSkills()
	if err != nil {
		return nil, err
	}
	return append(skills[:len(skills):len(skills)], builtin...), nil
}
