// Package assets embeds the mode table, the lead guide and the worker briefs.
package assets

import (
	"embed"
	"strings"
)

//go:embed config.json
var Config []byte

//go:embed guide.md
var Guide string

//go:embed reference.md
var Reference string

//go:embed dev.md
var Command string

//go:embed skill.md
var Skill string

//go:embed guide-codex.md
var GuideCodex string

//go:embed skill-openai.yaml
var SkillPolicy string

//go:embed prompts/*.md
var prompts embed.FS

//go:embed profiles/*.json
var profiles embed.FS

// Profile returns a built-in profile: a complete alternative mode table, such as "codex".
func Profile(name string) ([]byte, bool) {
	raw, err := profiles.ReadFile("profiles/" + name + ".json")
	return raw, err == nil
}

// Profiles lists the built-in profiles.
func Profiles() []string {
	entries, _ := profiles.ReadDir("profiles")
	var out []string
	for _, entry := range entries {
		out = append(out, strings.TrimSuffix(entry.Name(), ".json"))
	}
	return out
}

// Prompt returns a worker brief by name, for example "review".
func Prompt(name string) string {
	raw, err := prompts.ReadFile("prompts/" + name + ".md")
	if err != nil {
		panic(err)
	}
	return string(raw)
}
