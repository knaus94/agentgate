# Command-line interface

[Русский](ru/cli.md)

AgentGate has two related interfaces. A person starts work in a lead session with
`/dev <task>` in Claude Code or `$dev <task>` in Codex. The lead runs the CLI's
structured commands to prepare a plan, start the task and settle what only a person
can decide; one command, `run`, does everything between those decisions. The
terminal CLI also provides installation, diagnostics, configuration and observation.

## Discover commands

```sh
agentgate help
agentgate help task
agentgate help setup install
agentgate start --help
agentgate --version
```

`help`, `-h` and `--help` explain a command without running it. Help works even
when configuration is broken or the named input file does not exist. At the CLI
level `-h` always means help; heavy mode belongs in the request text passed to
`parse` or `preview`, or in the lead's `/dev -h …` request.

The grouped forms make related commands easier to find. Existing flat forms
remain supported and produce the same data:

| Grouped form | Equivalent flat form | Purpose |
|---|---|---|
| `agentgate setup install --lead both` | `agentgate install --lead both` | Install lead integrations |
| `agentgate setup doctor` | `agentgate doctor` | Check the environment |
| `agentgate setup update --check` | `agentgate update --check` | Check for a release |
| `agentgate task watch` | `agentgate watch` | Open the live dashboard |
| `agentgate task status` | `agentgate status` | Read current task status |
| `agentgate task stop` | `agentgate stop` | Request cooperative cancellation |
| `agentgate task start --plan wallet` | `agentgate start --plan wallet` | Open a task from a saved plan and run it |
| `agentgate task run` | `agentgate run` | Take every determined step until a person is needed |

`setup` also groups `config`, `accounts` and `modes`. `task` includes the lead
protocol commands shown by `agentgate help task`. Plans and design tools remain
separate top-level commands.

## Setup and configuration

```sh
agentgate setup install --lead both
agentgate setup doctor
agentgate config show
agentgate config validate
agentgate modes
agentgate accounts discover
agentgate accounts discover --save
```

Use `--lead codex` for Codex-only workers, `--lead claude` for Claude Code as the
lead, and `--lead both` for both lead integrations. See
[configuration](configuration.md) and [accounts](accounts.md) for model routes,
profiles, quotas and paid API credentials.

`config validate` reads the effective layered configuration and exits nonzero if
it is invalid. Configuration edits still use `config set`, `unset` and `profile`.

## The lead protocol

| Command | Purpose |
|---|---|
| `prepare` | Open a task the session writes: the working tree is recorded and the answer says what the plan needs in this mode. With `--workers`: a read-only worker drafts or revises the plan, and continues with answers and notes |
| `plan save`, `list`, `show`, `link` | Keep reusable plans in the repository |
| `route`, `preview` | Mode decision and routes, read-only |
| `architecture` | The plan and the session's design are read before the work: `--plan`, `{plan, design, alternatives, risks}`, then `{revise, plan, answered}`. With `--workers`: the design pass of a mode with architects; with `{revise, answers, notes}` the architect continues its design |
| `start [--implement]` | Open a task from a plan and run it |
| `run` | Check, read, accept and open the next stage — until the session or a person is needed. `{written}` closes the stage the session wrote; `{adopt}` and `{disown}` settle files changed outside the plan. With `--workers` it also runs the writers |
| `advise` | Put a question to the advisor, the model that is shown the conversation |
| `take` | On the user's word: continue in this session a task another session was writing |
| `answer` | Reply to a blocked writer in its own session, then run on |
| `decide` | Rule on findings, give the lead's own review, give work to the writers of named streams; then run on |
| `amend` | Change the plan of the task — named operations or the complete plan — with a reason; an addition waits for a running command, and a finished task gets a new stage |
| `status [--full]`, `show <part>` | The compact picture, or one part in full: findings, plan, stage, checks, review, architecture, receipt |
| `pause`, `resume`, `stop` | Park the task with the true reason; lift a pause; cancel the running operation |
| `check`, `visual`, `land` | Run declared checks; browser verification; land an isolated task |
| `drop`, `forget` | On the user's word: end a task and take back what it changed; remove a finished task from the record |

Every one of them answers with the same compact JSON object: the `step` the task is at, what this call
`did`, what only a person can settle (`needs`, each with the one way to `resolve` it and the `command` and
`input` that do it), and the `actions` that are legal now. What a command has of its own is under `result`;
a refused command carries `refused`, a failed one `error`. `agentgate guide` is the workflow for a lead;
`agentgate reference <page>` has the pages by situation, and `agentgate install` installs them as the skill
`agentgate`.

The commands of the earlier protocol — `launch`, `review`, `triage`, `fix`, `finish`, `replan`, `hold` —
answer with the name of what replaced them, and so do the `decide` inputs replaced in 0.17. `continue` is
accepted as another name for `run`.

## Task input

Most lead protocol commands accept one JSON object:

```sh
agentgate task start --file plan.json
agentgate task start --file=plan.json
agentgate task start --plan wallet
agentgate task check --all
```

`--file -` reads the object from standard input. `--file` and `--plan` are mutually
exclusive; `--plan` is available only where a plan is expected. Inputs over 16 MiB,
unknown fields, `null`, arrays, extra JSON values and trailing non-whitespace
content are rejected. Unknown flags, invalid subcommands and unexpected
positional arguments are errors. No flag is silently ignored.

The first `prepare` of a task takes the user's request as it was typed, in a file or on stdin; text that
is not a JSON object is the request. With `--workers` its later calls take a JSON object with `answers` or
`notes`.

The request carries the flags of the task: a mode (`-l`, `-m`, `-h`, `-x`), `--grill` or `--no-grill`,
`--step`, and `--workers` for sandboxed writers — with them also `-w` (an isolated worktree), `-N` (how many
workers at once), `--staged` and `--single`.

`parse` and `preview` accept request text from an argument, a file or stdin:

```sh
agentgate parse 'fix the pagination limit'
agentgate preview -- '-h move invoice totals to integer cents'
```

Use `--` before text that begins with `-`. Request text and `--file` cannot be
combined. These commands parse the request as data and do not execute shell
characters contained in it.

A Codex lead may need to invoke a mutating command as one plain command with a
JSON file so that the installed permission rule applies. The CLI explains this
when it detects confinement; see [Codex](codex.md).

## Stop and continue

`task stop` requests cancellation of a live operation. It does not unconditionally
kill an arbitrary process from a stale PID file. A task stopped this way is paused
by the user: only `task resume`, on the user's word, lifts it.

An interrupted writer round keeps its completed streams. `task run` retries the
unfinished ones using the recorded plan; a worker waiting for an answer needs
`task answer --file answer.json`. A process that died leaves no writer "running":
its streams are marked failed and the next `run` retries them. The tool does not
override a user stop or invent an answer to a worker's blocker.

`run` with nothing settled changes nothing and starts no model: it reports the
same `needs`. The one thing it repeats is a failing check whose cause may lie
outside the code, such as a stand that was down.

## Observation and scripts

```sh
agentgate task watch
agentgate task watch --all
agentgate task watch --once --no-color
agentgate task watch --json
agentgate task status --all
```

`watch` becomes a one-shot text snapshot when stdin or stdout is not a terminal,
and when `TERM=dumb`. `--once` explicitly requests a snapshot. `--json` always
prints one structured snapshot and exits; `--all` selects every checkout. JSON
snapshots contain `schema_version`, `scope`, `read_only` and `tasks`, with each
task's step, pause, workers, checks, blocking count and suggested next action. They do
not require a model or make provider quota requests.

`NO_COLOR` or `watch --no-color` disables colors without disabling keyboard
navigation. See [the dashboard](watch.md) for all keys and details.

Most lead commands already produce JSON. Their machine-readable output remains
on stdout; errors go to stderr. Help, guides, the model table, account tables and
plain `watch` output are human-readable text. `accounts --json` and
`watch --json` select structured observations explicitly.

| Exit code | Meaning |
|---|---|
| `0` | The requested command succeeded |
| `1` | Invalid input, configuration, environment or execution error; or a command that is not legal in the task's present state — then stdout holds the task's picture with `refused` and the legal `actions` |
| `2` | Declared checks failed in `check` |

The separate Node design tools document their own exit codes in `--help`.
A successful invocation of `status`, `route` or `preview` does not mean that a
task has passed its quality gate; inspect the returned `step` and `needs`.

## Design tools

```sh
agentgate figma --help
agentgate layout --help
agentgate fixtures --help
```

These commands retain their dedicated parsers, streaming output and detailed
options. Their help describes the complete syntax for Figma export, local
measurement and GraphQL fixture serving. See [design](design.md) for the full
workflow and its requirements.
