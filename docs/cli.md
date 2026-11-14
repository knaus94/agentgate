# Command-line interface

[Русский](ru/cli.md)

AgentGate has two related interfaces. A person starts work in a lead session with
`/dev <task>` in Claude Code or `$dev <task>` in Codex. The lead runs the CLI's
structured commands to prepare, execute, check and finish the task. The terminal
CLI also provides installation, diagnostics, configuration and observation.

## Discover commands

```sh
agentgate help
agentgate help task
agentgate help setup install
agentgate launch --help
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
| `agentgate task continue` | `agentgate continue` | Retry unfinished streams after interruption |
| `agentgate task launch --plan wallet` | `agentgate launch --plan wallet` | Launch a saved plan |

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

## Task input

Most lead protocol commands accept one JSON object:

```sh
agentgate task launch --file plan.json
agentgate task launch --file=plan.json
agentgate task launch --plan wallet
agentgate task check --all
```

`--file -` reads the object from standard input. `--file` and `--plan` are mutually
exclusive; `--plan` is available only where a plan is expected. Inputs over 16 MiB,
unknown fields, `null`, arrays, extra JSON values and trailing non-whitespace
content are rejected. Unknown flags, invalid subcommands and unexpected
positional arguments are errors. No flag is silently ignored.

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
kill an arbitrary process from a stale PID file. `task status` reports the state
and the next permitted action.

An interrupted writer round keeps its completed streams. After the cause is
resolved, `task continue` retries the unfinished streams using the recorded plan.
A held task first needs `task resume`; a worker waiting for an answer needs
`task answer --file answer.json`. The tool does not automatically override a user
stop or invent an answer to a worker's blocker.

The gate continues to enforce evidence freshness and review requirements. The
new command does not bypass checks, review, triage or final acceptance.

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
task's phase, workers, checks, blocking count and suggested next action. They do
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
| `1` | Invalid input, configuration, environment or execution error |
| `2` | Declared checks failed in `check` |

The separate Node design tools document their own exit codes in `--help`.
A successful invocation of `status`, `route` or `preview` does not mean that a
task has passed its quality gate; inspect the returned phase and requirements.

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
