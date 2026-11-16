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

//go:embed skill/*.md
var skill embed.FS

// Reference returns one page of the lead's pages; the empty topic is the index with the commands. A lead opens
// the page of its situation, so the plan schema or the design workflow is not carried through a task that
// never touches them. The same pages are installed as a skill.
func Reference(topic string) (string, bool) {
	if topic == "" || topic == "index" {
		_, body, _ := strings.Cut(SkillPage("SKILL"), "\n---\n")
		return strings.TrimLeft(body, "\n"), true
	}
	if strings.ContainsAny(topic, "/.") || topic == "SKILL" {
		return "", false
	}
	raw, err := skill.ReadFile("skill/" + topic + ".md")
	return string(raw), err == nil
}

// ReferenceTopics lists the pages besides the index.
func ReferenceTopics() []string {
	entries, _ := skill.ReadDir("skill")
	var out []string
	for _, entry := range entries {
		if name := strings.TrimSuffix(entry.Name(), ".md"); name != "SKILL" {
			out = append(out, name)
		}
	}
	return out
}

// SkillPage is one file of the skill, by name without its extension.
func SkillPage(name string) string {
	raw, _ := skill.ReadFile("skill/" + name + ".md")
	return string(raw)
}

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
