package dev

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// What the work needs that no writer makes — a database, a running service, a client generated from a
// migration a stream edits — is in the plan as its prerequisites. The lead makes each exist and says so with
// `run {prepared}`. Until then the tool holds back what needs it: the writers of a stage, or, for one made from
// what a stream writes, the checks after that stream. A writer is never left to discover a missing database.

// unprepared are the prerequisites the present position waits for. Before its writers start, only those made
// from no stream count; after, also those made from a stream that has written.
func (g *Gate) unprepared(beforeWriting bool) []Prerequisite {
	index := g.stageIndex()
	if index < 0 {
		return nil
	}
	var out []Prerequisite
	for _, item := range g.Plan.Prerequisites {
		if g.Plan.prerequisiteStage(item) != index || g.Prepared[item.ID] != 0 {
			continue
		}
		if item.After != "" && (beforeWriting || !g.wrote(item.After)) {
			continue
		}
		out = append(out, item)
	}
	return out
}

// wrote says whether a stream of the present stage has completed a round.
func (g *Gate) wrote(stream string) bool {
	return slices.ContainsFunc(g.Writers, func(writer *Writer) bool { return writer.Stream == stream && writer.Status == "COMPLETED" })
}

// madeFrom are the prerequisites the lead makes from what a stream writes: its writer is told not to wait for them.
func (g *Gate) madeFrom(stream string) []Prerequisite {
	var out []Prerequisite
	for _, item := range g.Plan.Prerequisites {
		if item.After == stream && g.Plan.prerequisiteStage(item) == g.stageIndex() && g.Prepared[item.ID] == 0 {
			out = append(out, item)
		}
	}
	return out
}

// prerequisiteNeeds are what the lead has to make exist before the work can go on.
func prerequisiteNeeds(items []Prerequisite) []Need {
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.ID
	}
	out := []Need{}
	for _, item := range items {
		need := Need{Kind: "setup", ID: item.ID, What: "prerequisite " + item.ID + ": " + item.What, Command: "run",
			Input: map[string]any{"prepared": ids},
			Resolve: "Make it exist: run its command, with the user's word where the plan's authorizations ask for one. Then `run {prepared: [ids]}` " +
				"says it exists, and what needs it starts. Nothing that needs it runs before."}
		if item.After != "" {
			need.What += " — made from what stream " + item.After + " wrote; the checks wait for it"
		}
		if len(item.Argv) > 0 {
			need.Detail = map[string]any{"command": item.Argv}
		}
		out = append(out, need)
	}
	return out
}

// markPrepared records the lead's word that prerequisites exist.
func (g *Gate) markPrepared(ids []string) (string, error) {
	var known []string
	for _, item := range g.Plan.Prerequisites {
		known = append(known, item.ID)
	}
	for _, id := range ids {
		if !slices.Contains(known, id) {
			if len(known) == 0 {
				known = []string{"none"}
			}
			return "", fmt.Errorf("%q is not a prerequisite of the plan; its prerequisites: %s", id, strings.Join(known, ", "))
		}
	}
	if g.Prepared == nil {
		g.Prepared = map[string]int64{}
	}
	for _, id := range ids {
		g.Prepared[id] = time.Now().Unix()
	}
	return "prepared by the lead: " + strings.Join(ids, ", "), nil
}

// restoreForeign puts files no stream owns back as the present position found them. It is for an isolated task's own
// worktree only: in a shared checkout such a file is somebody else's edit.
func (g *Gate) restoreForeign(paths []string) (string, error) {
	if g.Worktree == "" {
		return "", errors.New("restore is for an isolated task's own worktree; in a shared checkout a file no stream owns is somebody else's edit")
	}
	now, err := g.snapshot()
	if err != nil {
		return "", err
	}
	_, foreign, err := g.changes(now)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		if !slices.Contains(foreign, path) {
			if len(foreign) == 0 {
				foreign = []string{"none"}
			}
			return "", fmt.Errorf("%s is not a file the task changed outside its plan; those are: %s", path, strings.Join(foreign, ", "))
		}
	}
	for _, path := range paths {
		content, mode, found := treeFile(g.objects(), g.origin(), path)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		switch {
		case !found:
			continue // the position found no such file
		case mode == 0: // a symbolic link: its content is the target
			err = os.Symlink(string(content), path)
		default:
			if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				err = os.WriteFile(path, content, os.FileMode(mode))
			}
		}
		if err != nil {
			return "", err
		}
	}
	return "restored as the position found them: " + strings.Join(paths, ", "), nil
}
