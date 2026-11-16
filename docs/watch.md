# The terminal dashboard

[Русский](ru/watch.md) · [CLI reference](cli.md)

`agentgate task watch` opens a live, read-only view of tasks from Claude Code and
Codex. Run it inside a repository or use the `agentgate: watch` Zed task. The
compatible `agentgate watch` command opens the same dashboard.

The dashboard does not drive a task, start a model or write gate state. The one
thing it asks a provider for is the limits of the configured accounts, as
`agentgate accounts` does. Its purpose is to make the current step, what the task
waits for, worker activity, the next action and the room left on each
subscription visible in one place.

## What it shows

| Section | Contents |
|---|---|
| Tasks | Running tasks first, then tasks needing attention, isolated changes ready to land, and recent finished tasks. Each task shows mode, step or pause, elapsed time and the operation in flight |
| Stages | Accepted, current and remaining parts of a staged task; recorded operations and their durations |
| Workers | The preparation worker, designers, consultants, writers and reviewers; model, effort, account, elapsed time, retries and most recent activity |
| Checks | Declared verification commands, their latest results and whether the evidence still matches current inputs |
| Findings | Open findings by severity, notes and the number blocking completion |
| Signals | Mechanical hints on added lines that reviewers should investigate |
| Plan | Brief, streams, owned files and accepted architecture |
| Limits | One line per configured account: provider, name, plan, every limit by name, and what the tool does with the account |
| Header | Current scope and, per account, its tightest limit by name: `main fable 72% · work2 all 12%`. Dropped below 70 columns |

### Limits

```
 LIMITS                                                                                        read 2m05s ago
  claude  main        max 20x     all 49% · fable 72%  kept for the lead · fable: unknown if all caps it
  claude  work2       max 5x      all 12% · fable 100% (back Nov 17 04:00)  fable resting until Nov 17 04:00
                                  fable: own limit only
  codex   codex       prolite     5h 8% · week 66%
```

`all` is the general weekly limit of a subscription that also has a limit for one model, `week` the weekly
limit of one that has none, a model's name its own limit, `5h` the session window. A limit at 80% or more
shows when it resets. After the limits comes what the tool does with the account, in few words: kept for
the lead, resting and until when, not logged in, the same subscription as another account, and what is
known of a model's own limit ([accounts](accounts.md#limits-a-model-has-of-its-own); `agentgate accounts`
says the same in full sentences). Notes that do not fit beside the limits continue on the next line.
`read … ago` is the oldest reading shown.

The block gives way before the tasks do: under 24 rows, or when the tasks and the selected task fill the
screen, only the header figure remains. `L` hides and shows it. Without configured accounts there is no
block.

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
| `L` | Hide or show the limits of the accounts |
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
| Findings | Severity, location, evidence, impact, suggested remedy, the decision on it and the author's dispute |
| Signals | Potential issues detected mechanically, without treating them as confirmed defects |
| Plan | Brief, each stream's assignment and files, accepted design |

Activity is parsed from each CLI's event stream. Long internal paths are shortened
to `task:/…`, `worktree:/…` and `state:/…`. Terminal control sequences from worker
output are removed before rendering, including clipboard and title commands.
Pasted text is not interpreted as navigation shortcuts.

## Refresh and freshness

Worker activity and state refresh every second, from files read again only when
they changed: a worker's event stream is read on from where the last read ended,
and between changes a redraw reads no file content. Check freshness — which takes
one record of the working tree with git — is judged when the task's state changes,
when `r` is pressed, and every 30 seconds for an open task while no operation
runs; a running operation verifies by itself. Moving between rows does not rescan
the repository.

A running operation says what it is busy with under the task's row — `now: check
4 of 7: yarn nx run client:test …`, `writing: api (sol:xhigh)`, `review:
correctness (opus:high)` — and for how long; `status` has the same under
`running.now`, and `watch --json` under each task's `now`. The quality gate itself always revalidates the evidence when a
command depends on it; the dashboard's display cache cannot authorize an acceptance.

The limits shown are the ones saved by the last reading, whoever took it: a worker
call, `agentgate accounts` or the dashboard. While the dashboard is open it reads
again the limits of an account whose reading is older than five minutes, one
account at a time, in the background; the redraw never waits for a provider. It is
the same request `agentgate accounts` makes — for Claude a usage request with the
profile's token, for Codex the profile's local `codex app-server` — and it starts
no model. A reading that fails keeps the previous numbers and adds the reason to
the account's line. `r` does not ask the providers; `agentgate accounts` does, at
once. `--once` and `--json` print what is saved and ask nothing.

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
`schema_version` (currently `2`), `read_only`, `scope`, a `tasks` array and an
`accounts` array. Task
objects include `step`, `paused`, the position among the stages, running state,
workers, checks, blocking count and next action. Version 1 had a `phase` in place
of `step` and `paused`. Invalid task files appear with an `error` field. The snapshot does not
include internal Gate objects or full worker prompts. `accounts` holds the configured
accounts with their saved readings, in the shape of `accounts list --json`: `windows`
with `label`, `scope`, `period`, `used_percent` and `resets_at`, `cooling`, `lead_only`,
`general_caps` and `probed_at`.

Without `--all`, interactive and text modes automatically show all checkouts when
the current checkout has no tasks and other checkouts do. JSON mode keeps the
explicit scope: pass `--all` to request every checkout. `task status --all`
provides the existing checkout overview in its original JSON format.
