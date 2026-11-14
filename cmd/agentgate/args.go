package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/knaus94/agentgate/internal/dev"
)

type arguments struct {
	action string
	file   string
	plan   string
	flags  map[string]bool
	rest   []string
	lead   string
	help   []string
}

func parseArgs(args []string) (arguments, error) {
	out := arguments{flags: map[string]bool{}}
	options := true
	for index := 0; index < len(args); index++ {
		value := args[index]
		if options && value == "--" {
			options = false
			continue
		}
		if options && (value == "--help" || value == "-h") {
			out.flags["help"] = true
			continue
		}
		if options && (value == "--version" || value == "-v") {
			out.flags["version"] = true
			continue
		}
		if options && strings.HasPrefix(value, "--") {
			name, parameter, supplied := strings.Cut(value[2:], "=")
			if name == "file" || name == "lead" || name == "plan" {
				if out.flags[name] {
					return out, fmt.Errorf("--%s may only be supplied once", name)
				}
				if !supplied {
					if index+1 >= len(args) || (strings.HasPrefix(args[index+1], "-") && args[index+1] != "-") {
						return out, fmt.Errorf("--%s needs a value", name)
					}
					index++
					parameter = args[index]
				}
				if strings.TrimSpace(parameter) == "" {
					return out, fmt.Errorf("--%s needs a nonempty value", name)
				}
				out.flags[name] = true
				switch name {
				case "file":
					out.file = parameter
				case "lead":
					out.lead = parameter
				case "plan":
					out.plan = parameter
				}
				continue
			}
			if supplied {
				return out, fmt.Errorf("--%s does not accept a value", name)
			}
			out.flags[name] = true
			continue
		}
		if options && strings.HasPrefix(value, "-") && value != "-" {
			return out, fmt.Errorf("unknown option %q; use -- before request text beginning with -", value)
		}
		if out.action == "" {
			out.action = value
		} else {
			out.rest = append(out.rest, value)
		}
	}

	// Help is resolved before configuration, plan IO, migrations, or mutations.
	if out.action == "help" || out.flags["help"] {
		out.help = out.rest
		if out.action != "help" && out.action != "" {
			out.help = append([]string{out.action}, out.rest...)
		}
		out.action = "help"
		return out, nil
	}
	if out.flags["version"] {
		if out.action != "" || len(out.flags) != 1 {
			return out, errors.New("--version must be used on its own")
		}
		out.action = "version"
		delete(out.flags, "version")
	}
	if group := groupCommands(out.action); group != nil {
		if len(out.rest) == 0 {
			out.help, out.action = []string{out.action}, "help"
			return out, nil
		}
		if !slices.Contains(group, out.rest[0]) {
			return out, fmt.Errorf("unknown %s command %q; run `agentgate help %s`", out.action, out.rest[0], out.action)
		}
		out.action, out.rest = out.rest[0], out.rest[1:]
	}
	if out.action == "" {
		if len(out.flags) != 0 {
			return out, errors.New("an option needs a command; run `agentgate help`")
		}
		return out, nil
	}
	if out.action == "limits" { // retained compatibility alias
		out.action = "accounts"
	}
	spec, ok := command(out.action)
	if !ok {
		return out, fmt.Errorf("unknown command %q; run `agentgate help`", out.action)
	}
	for name := range out.flags {
		if (name == "file" && spec.input) || (name == "plan" && spec.plan) || slices.Contains(spec.flags, name) {
			continue
		}
		return out, fmt.Errorf("unknown option --%s for %s; run `agentgate help %s`", name, out.action, out.action)
	}
	if out.file != "" && out.plan != "" {
		return out, errors.New("--file and --plan are mutually exclusive")
	}
	if out.lead != "" && !slices.Contains([]string{"claude", "codex", "both"}, out.lead) {
		return out, errors.New("--lead must be claude, codex or both")
	}
	if err := out.validatePositionals(); err != nil {
		return out, err
	}
	return out, nil
}

func (a arguments) validatePositionals() error {
	fail := func() error {
		return fmt.Errorf("invalid arguments for %s; run `agentgate help %s`", a.action, a.action)
	}
	switch a.action {
	case "parse", "preview":
		if len(a.rest) > 0 && a.file != "" {
			return errors.New("request text and --file are mutually exclusive")
		}
	case "config":
		if len(a.rest) == 0 {
			return nil
		}
		count, ok := map[string]int{"show": 1, "path": 1, "init": 1, "validate": 1, "extensions": 1, "profiles": 1, "profile": 2, "unset": 2, "set": 3}[a.rest[0]]
		if !ok || len(a.rest) != count {
			return fail()
		}
	case "plan":
		action := "list"
		if len(a.rest) > 0 {
			action = a.rest[0]
		}
		if action != "save" && (a.file != "" || a.plan != "") {
			return errors.New("--file and --plan apply only to plan save")
		}
		switch action {
		case "list":
			if len(a.rest) > 1 {
				return fail()
			}
		case "save", "link":
			if len(a.rest) > 2 {
				return fail()
			}
		case "show":
			if len(a.rest) != 2 {
				return fail()
			}
		default:
			return fail()
		}
	case "accounts":
		action := "list"
		if len(a.rest) > 0 {
			action = a.rest[0]
		}
		count, ok := map[string]int{"list": 1, "limits": 1, "discover": 1, "add": 4, "remove": 2, "reset": 2}[action]
		if !ok || (len(a.rest) != count && !(action == "list" && len(a.rest) == 0)) {
			return fail()
		}
		for flag := range a.flags {
			valid := flag == "json" && (action == "list" || action == "limits") || flag == "save" && action == "discover" || flag == "paid" && action == "add"
			if !valid {
				return fmt.Errorf("--%s does not apply to accounts %s", flag, action)
			}
		}
	default:
		if len(a.rest) != 0 {
			return fail()
		}
	}
	return nil
}

const maxInputBytes = 16 << 20

func readInput(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxInputBytes {
		return nil, fmt.Errorf("input exceeds %d MiB", maxInputBytes>>20)
	}
	return raw, nil
}

func (a arguments) read() ([]byte, error) {
	path := a.file
	if a.plan != "" {
		var err error
		path, err = dev.PlanFile(a.plan)
		if err != nil {
			return nil, err
		}
	}
	switch path {
	case "":
		return nil, nil
	case "-":
		return readInput(os.Stdin)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readInput(file)
}

func decodeInput(raw []byte, into any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("expected one JSON object (not null, an array or a scalar)")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("expected one JSON object; found a second value")
		}
		return fmt.Errorf("unexpected trailing input: %w", err)
	}
	return nil
}

func (a arguments) input(into any, required bool) error {
	raw, err := a.read()
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		if a.file != "" || a.plan != "" {
			return fmt.Errorf("%s input: expected a nonempty JSON object", a.action)
		}
		if required {
			return fmt.Errorf("%s needs --file <path> or --file -", a.action)
		}
		return nil
	}
	if err := decodeInput(raw, into); err != nil {
		return fmt.Errorf("%s input: %w", a.action, err)
	}
	return nil
}

func (a arguments) raw() (string, error) {
	if len(a.rest) > 0 {
		return strings.Join(a.rest, " "), nil
	}
	if a.file == "" {
		a.file = "-"
	}
	raw, err := a.read()
	return string(raw), err
}
