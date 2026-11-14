# The terminal dashboard

[Русский](ru/watch.md) · [CLI reference](cli.md)

`agentgate task watch` opens a live, read-only view of tasks from Claude Code and
Codex. Run it inside a repository or use the `agentgate: watch` Zed task. The
compatible `agentgate watch` command opens the same dashboard.

The dashboard does not drive a task, start a model, refresh a subscription quota
or write gate state. Its purpose is to make the current phase, worker activity
and the next action visible in one place.

## What it shows

| Section | Contents |
|---|---|
| Tasks | Running tasks first, then tasks needing attention, isolated changes ready to land, and recent finished tasks. Each task shows mode, phase, elapsed time and the operation in flight |
| Stages | Accepted, current and remaining parts of a staged task; recorded operations and their durations |
| Workers | Scouts, designers, consultants, writers and reviewers; model, effort, account, elapsed time, retries and most recent activity |
| Checks | Declared verification commands, their latest results and whether the evidence still matches current inputs |
| Findings | Open findings by severity, notes and the number blocking completion |
| Signals | Mechanical hints on added lines that reviewers should investigate |
| Plan | Brief, streams, owned files and accepted architecture |
| Header | Current scope and last recorded subscription usage; the dashboard never calls providers to obtain fresh limits |

At narrow widths, task and worker rows use two lines. At small heights, the
viewport follows the selected row, so a check or worker remains reachable even
when it falls below the initial screen. Terminal resize is handled while the
panel is open. A malformed or unreadable task state appears as an explicit error
row instead of crashing the panel or pretending no task exists.

## Keyboard

| Key | Action |
|---|---|
| `↑` / `↓` or `k` / `j` | Move between tasks or detail rows |
| `Tab`, `←` / `→` or `h` / `l` | Switch between the task list and its rows |
| `Enter` | Enter the selected task or open a detail row |
| `Esc` / `h` | Return from details |
| `PgUp` / `PgDn`, `Space` | Move by a page |
| `Home` / `End`, `g` / `G` | Move to the first or last row; in details, the last line resumes following |
| `f` | Follow new worker activity in an open detail view |
| `a` | Toggle between this checkout and every checkout |
| `r` | Refresh immediately, including evidence freshness |
| `?` | Show keyboard help |
| `q` / `Ctrl-C` | Close the dashboard |

Opening a worker follows its latest activity. Scrolling stops following; `f`
resumes it. The selected worker is identified by its receipt directory, so an
incoming review round cannot silently replace the worker currently being read.

| Opened row | Detail |
|---|---|
| Worker | Commands with the end of their output, files read and edited, model messages and final report |
| Check | Output from its latest recorded run |
| Findings | Severity, location, evidence, impact, suggested remedy and triage decision |
| Signals | Potential issues detected mechanically, without treating them as confirmed defects |
| Plan | Brief, each stream's assignment and files, accepted design |

Activity is parsed from each CLI's event stream. Long internal paths are shortened
to `task:/…`, `worktree:/…` and `state:/…`. Terminal control sequences from worker
output are removed before rendering, including clipboard and title commands.
Pasted text is not interpreted as navigation shortcuts.

## Refresh and freshness

Worker activity and state refresh every second. Expensive check freshness
calculations refresh at most five seconds apart, immediately when relevant gate
state changes, and when `r` is pressed. Moving between rows does not rescan the
repository. The quality gate itself always revalidates the evidence when a
command depends on it; the dashboard's display cache cannot authorize a finish.

## Text and JSON snapshots

```sh
agentgate task watch --once
agentgate task watch --once --no-color
agentgate task watch --all
agentgate task watch --json
agentgate task watch --all --json
```

`--once` prints one text snapshot and exits. Without a terminal on either input or
output, `watch` also takes a snapshot instead of waiting for keys. `TERM=dumb`
uses the snapshot mode. `NO_COLOR` or `--no-color` disables ANSI colors while
retaining keyboard navigation on a normal terminal.

`--json` is always a single snapshot and is suitable for scripts. It contains a
`schema_version` (currently `1`), `read_only`, `scope`, and a `tasks` array. Task
objects include phase, running state, workers, checks, blocking count and next
action. Invalid task files appear with an `error` field. The snapshot does not
include internal Gate objects or full worker prompts.

Without `--all`, interactive and text modes automatically show all checkouts when
the current checkout has no tasks and other checkouts do. JSON mode keeps the
explicit scope: pass `--all` to request every checkout. `task status --all`
provides the existing checkout overview in its original JSON format.
