package dev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	designkit "github.com/knaus94/agentgate/design-kit"
	"github.com/knaus94/agentgate/internal/ops"
)

// DesignRef says that a stream implements a design package, and where the result is shown. From it the gate
// hands the brief to that stream alone, measures the page before the work and after it, blocks what got worse,
// and shows a reviewer the design next to the result.
type DesignRef struct {
	Package      string `json:"package"`                 // a directory written by `agentgate figma pull`
	URL          string `json:"url"`                     // the page that shows the result, on a local stand
	Root         string `json:"root,omitempty"`          // the element that implements the pulled node
	Stream       string `json:"stream,omitempty"`        // who builds it; default: the plan's only stream
	Map          string `json:"map,omitempty"`           // what the product shows differently, with reasons
	StorageState string `json:"storage_state,omitempty"` // a browser session of the stand
	Locale       string `json:"locale,omitempty"`
	Viewport     string `json:"viewport,omitempty"`
}

const layoutTimeout = 180

// layoutCommand is the binary that runs the layout check: this one. Tests replace it.
var layoutCommand = func() string {
	if self, err := os.Executable(); err == nil {
		return self
	}
	return "agentgate"
}

func (d DesignRef) digest() string {
	var manifest struct {
		Digest string `json:"digest"`
	}
	raw, _ := os.ReadFile(filepath.Join(d.Package, "manifest.json"))
	_ = json.Unmarshal(raw, &manifest)
	exceptions, _ := os.ReadFile(d.Map)
	return digest(map[string]any{"package": manifest.Digest, "map": string(exceptions), "url": d.URL, "root": d.Root, "viewport": d.Viewport})
}

// dir is where the measurements of one design in one checkout are kept: what was there before the task, and
// the pictures of the last check.
func (d DesignRef) dir() string {
	return filepath.Join(checkoutDir(""), "layout", digest(map[string]any{"package": d.Package, "url": d.URL, "root": d.Root})[:16])
}

func (d DesignRef) argv(extra ...string) []string {
	out := []string{layoutCommand(), "layout", d.Package, "--url", d.URL}
	for flag, value := range map[string]string{"--root": d.Root, "--map": d.Map, "--storage-state": d.StorageState, "--locale": d.Locale, "--viewport": d.Viewport} {
		if value != "" {
			out = append(out, flag, value)
		}
	}
	return append(pairsSorted(out[:5], out[5:]), extra...)
}

// pairsSorted keeps the argv of a check the same from run to run: a map does not.
func pairsSorted(head, pairs []string) []string {
	type pair struct{ flag, value string }
	var list []pair
	for index := 0; index+1 < len(pairs); index += 2 {
		list = append(list, pair{pairs[index], pairs[index+1]})
	}
	slices.SortFunc(list, func(a, b pair) int { return strings.Compare(a.flag, b.flag) })
	out := slices.Clone(head)
	for _, item := range list {
		out = append(out, item.flag, item.value)
	}
	return out
}

func (d DesignRef) check() Check {
	return Check{Argv: d.argv("--baseline", filepath.Join(d.dir(), "baseline.json"), "--out", filepath.Join(d.dir(), "last")),
		TimeoutS: layoutTimeout, NoReuse: true}
}

// validateDesign checks every design of the plan and declares its layout check, once.
func (r *Resolved) validateDesign() error {
	plan := r.Plan
	for index := range plan.Design {
		design := &plan.Design[index]
		if _, err := os.Stat(filepath.Join(design.Package, "data", "spec.json")); err != nil {
			return fmt.Errorf("design %q is not a package written by `agentgate figma pull`", design.Package)
		}
		if design.Stream == "" && len(plan.Streams) == 1 {
			design.Stream = plan.Streams[0].Name
		}
		if !slices.ContainsFunc(plan.Streams, func(stream Stream) bool { return stream.Name == design.Stream }) {
			return fmt.Errorf("design %q: say which stream builds it", design.Package)
		}
		if strings.HasPrefix(design.URL, "http") {
			if _, err := standOrigins([]string{strings.SplitN(design.URL, "/", 4)[0] + "//" + strings.SplitN(design.URL, "/", 4)[2]}); err != nil {
				return err
			}
		} else if !strings.HasPrefix(design.URL, "file://") {
			return fmt.Errorf("design %q: url is a page of a local stand", design.Package)
		}
		if design.Map != "" {
			if _, err := os.Stat(design.Map); err != nil {
				return fmt.Errorf("design %q: the map %s is missing", design.Package, design.Map)
			}
		}
		check := design.check()
		if !slices.ContainsFunc(plan.Checks, func(other Check) bool { return slices.Equal(other.Argv, check.Argv) }) {
			plan.Checks = append(plan.Checks, check)
		}
	}
	return nil
}

// designBaselines measures every design's page before anyone writes: what differs already is not this task's.
func (g *Gate) designBaselines() error {
	for _, design := range g.Plan.Design {
		if err := os.MkdirAll(design.dir(), 0o700); err != nil {
			return err
		}
		argv := design.argv("--write-baseline", filepath.Join(design.dir(), "baseline.json"))
		var out bytes.Buffer
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := ops.Run(cmd, seconds(layoutTimeout)); err != nil {
			return fmt.Errorf("the page of design %q could not be measured before the work — is the stand up at %s? %s", design.Package, design.URL, firstLine(strings.TrimSpace(out.String())))
		}
	}
	return nil
}

// designFor is what the stream that builds a design is given: the brief and the picture, by path.
func (g *Gate) designFor(stream string) any {
	var out []map[string]any
	for _, design := range g.Plan.Design {
		if design.Stream != stream {
			continue
		}
		out = append(out, map[string]any{"brief": filepath.Join(design.Package, "brief.md"), "picture": filepath.Join(design.Package, "frame.png"),
			"assets": filepath.Join(design.Package, "assets"), "page": design.URL, "root": design.Root,
			"check": strings.Join(design.check().Argv, " "),
			"layer": "agentgate figma node " + design.Package + " <handle>"})
	}
	if out == nil {
		return nil
	}
	return out
}

// designReview is what a reviewer of the `design` axis is given: the design, the page as the last check saw
// it, and the exceptions someone wrote.
func (g *Gate) designReview() ([]map[string]any, []string) {
	var out []map[string]any
	var dirs []string
	for _, design := range g.Plan.Design {
		last := filepath.Join(design.dir(), "last")
		out = append(out, map[string]any{"brief": filepath.Join(design.Package, "brief.md"), "design_picture": filepath.Join(design.Package, "frame.png"),
			"page_picture": filepath.Join(last, "actual.png"), "differences_picture": filepath.Join(last, "diff.png"),
			"check_report": filepath.Join(last, "report.md"), "exceptions": design.Map, "stream": design.Stream})
		dirs = append(dirs, design.dir())
	}
	return out, dirs
}

func (g *Gate) designDigests() []string {
	var out []string
	for _, design := range g.Plan.Design {
		out = append(out, design.digest())
	}
	return out
}

var errNoDesignSkill = errors.New("the design skill is not built in")

func designSkill() (string, error) {
	text := designkit.Skill("design-to-code")
	if text == "" {
		return "", errNoDesignSkill
	}
	return text, nil
}
