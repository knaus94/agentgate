// Package assets embeds the mode table, the lead guide and the worker briefs.
package assets

import (
	"embed"
)

//go:embed config.json
var Config []byte

//go:embed guide.md
var Guide string

//go:embed reference.md
var Reference string

//go:embed dev.md
var Command string

//go:embed prompts/*.md
var prompts embed.FS

// Prompt returns a worker brief by name, for example "review".
func Prompt(name string) string {
	raw, err := prompts.ReadFile("prompts/" + name + ".md")
	if err != nil {
		panic(err)
	}
	return string(raw)
}
