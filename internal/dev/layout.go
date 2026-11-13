package dev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	designkit "github.com/knaus94/agentgate/design-kit"
	"github.com/knaus94/agentgate/internal/ops"
)

// DesignRef says that a stream implements a design package, and where the result is shown. From it the gate
// hands the brief to that stream alone, measures the page before the work and after it, blocks what got worse,
// and shows a reviewer the design next to the result.
type DesignRef struct {
	Figma        string `json:"figma,omitempty"`         // a link to the frame: the screen is made from it at launch when it is not there yet
	Name         string `json:"name,omitempty"`          // with figma: the screen's directory name; default: the node id
	Screen       string `json:"screen,omitempty"`        // a screen directory: screen.json, package/, fixtures/ and map.json. It needs nothing else here
	Package      string `json:"package,omitempty"`       // without a screen: a directory written by `agentgate figma pull`
	URL          string `json:"url,omitempty"`           // without a screen: the page that shows the result, on a local stand
	Root         string `json:"root,omitempty"`          // the element that implements the pulled node
	Stream       string `json:"stream,omitempty"`        // who builds it; default: the plan's only stream
	Map          string `json:"map,omitempty"`           // what the product shows differently, with reasons
	StorageState string `json:"storage_state,omitempty"` // a browser session of the stand
	Locale       string `json:"locale,omitempty"`
	Viewport     string `json:"viewport,omitempty"`
}

const layoutTimeout = 300

var figmaNode = regexp.MustCompile(`node-id=(\d+)(?:-|:|%3A)(\d+)`)

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
	if d.Screen != "" {
		screen, _ := os.ReadFile(filepath.Join(d.Screen, "screen.json"))
		exceptions = append(exceptions, screen...)
	}
	return digest(map[string]any{"package": manifest.Digest, "map": string(exceptions), "url": d.URL, "root": d.Root, "viewport": d.Viewport})
}

// dir is where the measurements of one design in one checkout are kept: what was there before the task, and
// the pictures of the last check.
func (d DesignRef) dir() string {
	return filepath.Join(checkoutDir(""), "layout", digest(map[string]any{"package": d.Package, "figma": d.Figma,
		"url": d.URL, "root": d.Root, "viewport": d.Viewport, "locale": d.Locale})[:16])
}

func (d DesignRef) argv(extra ...string) []string {
	target := d.Package
	if d.Screen != "" {
		target = d.Screen
	}
	out := []string{layoutCommand(), "layout", target}
	for flag, value := range map[string]string{"--url": d.URL, "--root": d.Root, "--map": d.Map, "--storage-state": d.StorageState, "--locale": d.Locale, "--viewport": d.Viewport} {
		if value != "" {
			out = append(out, flag, value)
		}
	}
	return append(pairsSorted(out[:3], out[3:]), extra...)
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
		TimeoutS: layoutTimeout, NoReuse: true, Generated: "design", Kind: "measurement"}
}

func (d DesignRef) imported() bool {
	if !fileExists(filepath.Join(d.Package, "data", "spec.json")) {
		return false
	}
	if d.Figma == "" {
		return true
	}
	var manifest struct{ Source struct{ Key, Node string } }
	raw, err := os.ReadFile(filepath.Join(d.Package, "manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		return false
	}
	link, err := url.Parse(d.Figma)
	if err != nil {
		return false
	}
	parts := strings.Split(strings.Trim(link.Path, "/"), "/")
	return len(parts) >= 2 && manifest.Source.Key == parts[1] &&
		manifest.Source.Node == strings.ReplaceAll(link.Query().Get("node-id"), "-", ":")
}

// Older saved plans contain expanded layout commands without an owner marker. Recognize only the gate's
// baseline/output pair, never an independent layout command or another user-declared check.
func generatedDesignCheck(check Check) bool {
	if check.Generated == "design" {
		return true
	}
	if !check.NoReuse || len(check.Argv) < 7 || check.Argv[1] != "layout" {
		return false
	}
	baseline, out := "", ""
	for i := 3; i+1 < len(check.Argv); i += 2 {
		switch check.Argv[i] {
		case "--baseline":
			baseline = check.Argv[i+1]
		case "--out":
			out = check.Argv[i+1]
		}
	}
	return filepath.Base(baseline) == "baseline.json" && filepath.Base(filepath.Dir(filepath.Dir(baseline))) == "layout" &&
		out == filepath.Join(filepath.Dir(baseline), "last")
}

// validateDesign checks every design of the plan and declares its layout check, once.
func (r *Resolved) validateDesign() error {
	plan := r.Plan
	plan.Checks = slices.DeleteFunc(slices.Clone(plan.Checks), generatedDesignCheck)
	for index := range plan.Design {
		design := &plan.Design[index]
		if design.Figma != "" && design.Screen == "" {
			name := design.Name
			if name == "" {
				if match := figmaNode.FindStringSubmatch(design.Figma); match != nil {
					name = match[1] + "-" + match[2]
				}
			}
			if name == "" || design.URL == "" || design.Root == "" {
				return errors.New("a design given by its figma link needs the link to a frame (node-id), `url` — the page that will show it — and `root`")
			}
			screens := r.Config.Design.Screens
			if screens == "" {
				screens = filepath.Join(".agentgate", "design")
			}
			design.Screen = filepath.Join(screens, strings.Trim(strings.Map(func(char rune) rune {
				if char == '-' || char == '_' || (char >= '0' && char <= '9') || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') {
					return char
				}
				return '-'
			}, name), "-"))
		}
		// A screen is made when it has both its description and its design; a link fills in what is missing.
		made := design.Screen != "" && fileExists(filepath.Join(design.Screen, "screen.json")) &&
			fileExists(filepath.Join(design.Screen, "package", "data", "spec.json"))
		if design.Screen != "" {
			if !made && design.Figma == "" {
				return fmt.Errorf("design %q is not a screen: it needs screen.json and package/, or a `figma` link to make them from", design.Screen)
			}
			design.Package = filepath.Join(design.Screen, "package")
			if exceptions := filepath.Join(design.Screen, "map.json"); design.Map == "" && fileExists(exceptions) {
				design.Map = exceptions
			}
		}
		if _, err := os.Stat(filepath.Join(design.Package, "data", "spec.json")); err != nil && (made || design.Figma == "") {
			return fmt.Errorf("design %q is not a package written by `agentgate figma pull`", design.Package)
		}
		if design.Stream == "" && len(plan.Streams) == 1 {
			design.Stream = plan.Streams[0].Name
		}
		if !slices.ContainsFunc(plan.Streams, func(stream Stream) bool { return stream.Name == design.Stream }) {
			return fmt.Errorf("design %q: say which stream builds it", design.Package)
		}
		if design.Screen != "" {
			// the screen's own files say where it is shown; the check reads them
		} else if strings.HasPrefix(design.URL, "http") {
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
	return g.prepareDesigns(true)
}

func (g *Gate) prepareDesigns(measureBefore bool) error {
	for _, design := range g.Plan.Design {
		if err := os.MkdirAll(design.dir(), 0o700); err != nil {
			return err
		}
		// A design named by its link becomes a screen here, once: the lead pulls nothing by hand.
		if design.Figma != "" && !design.imported() {
			argv := []string{layoutCommand(), "layout", "new", design.Screen, design.Figma, "--url", design.URL, "--root", design.Root}
			if design.Locale != "" {
				argv = append(argv, "--locale", design.Locale)
			}
			var out bytes.Buffer
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := ops.Run(cmd, seconds(layoutTimeout)); err != nil {
				return fmt.Errorf("the design %s could not be pulled: %s", design.Figma, lastLine(strings.TrimSpace(out.String())))
			}
		}
		if !measureBefore {
			continue
		}
		argv := design.argv("--write-baseline", filepath.Join(design.dir(), "baseline.json"))
		var out bytes.Buffer
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := ops.Run(cmd, seconds(layoutTimeout)); err != nil {
			return fmt.Errorf("the page of design %q could not be measured before the work: %s", design.Package, lastLine(strings.TrimSpace(out.String())))
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
			"assets":       filepath.Join(design.Package, "assets"),
			"verification": "The gate runs layout and sends measured differences back to this stream. Do not start the stand or edit its baselines, maps or tolerances.",
			"layer":        "agentgate figma node " + design.Package + " <handle>"})
	}
	if out == nil {
		return nil
	}
	return out
}

func (g *Gate) designReadDirs(stream string) []string {
	var dirs []string
	for _, design := range g.Plan.Design {
		if design.Stream == stream {
			path, _ := filepath.Abs(design.Package)
			dirs = append(dirs, path)
		}
	}
	return dirs
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

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return lines[len(lines)-1]
}

var errNoDesignSkill = errors.New("the design skill is not built in")

func designSkill() (string, error) {
	text := designkit.Skill("design-to-code")
	if text == "" {
		return "", errNoDesignSkill
	}
	return text, nil
}
