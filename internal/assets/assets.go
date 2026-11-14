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

//go:embed reference/*.md
var reference embed.FS

// Reference returns one topic of the protocol reference; the empty topic is the index: commands and inputs.
// A lead loads a topic when it needs it, so the plan schema or the design workflow is not carried through a
// task that never touches them.
func Reference(topic string) (string, bool) {
	if topic == "" {
		topic = "index"
	}
	raw, err := reference.ReadFile("reference/" + topic + ".md")
	return string(raw), err == nil && !strings.ContainsAny(topic, "/.")
}

// ReferenceTopics lists the topics besides the index.
func ReferenceTopics() []string {
	entries, _ := reference.ReadDir("reference")
	var out []string
	for _, entry := range entries {
		if name := strings.TrimSuffix(entry.Name(), ".md"); name != "index" {
			out = append(out, name)
		}
	}
	return out
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
