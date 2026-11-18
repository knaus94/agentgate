# AgentGate

[Русский](README.ru.md) · [Quick start](#quick-start) · [Commands](docs/cli.md) · [Architecture](docs/architecture.md) · [Release notes](RELEASE_NOTES.md)

**A local quality gate for work done in Claude Code and Codex.** Your own session plans the task and writes it
in small stages; AgentGate holds it to the plan — it runs the declared checks, has a second model read the
plan and the code where the mode says so, and allows completion only while that evidence still matches the
change. Sandboxed writers can do the work instead, with `--workers`.

AgentGate is a Linux CLI with a live terminal dashboard. It runs without a background daemon or a hosted
service. It uses the provider CLIs and accounts you configure. Former installations named `claude-dev` are
recognized for migration.

## Quick start

### From the complete release archive

The archive includes source, documentation, SHA-256 checksums, and binaries for Linux x86-64 and arm64.
After extracting it:

```sh
cd agentgate-0.22.0
sh scripts/install-local.sh --lead both
agentgate setup doctor
agentgate config validate
agentgate modes
```

Choose `--lead codex`, `--lead claude` or `--lead both`. Codex-only installation selects the built-in `codex`
worker profile when no profile is already selected; an existing custom profile is preserved.
`--no-integrations` copies the binary without changing your lead configuration. The local installer uses
no network and requires no root access; your chosen directory must be writable and on `PATH`.

Required: Linux (including WSL2), `git`, `diff`, `bubblewrap` (`bwrap`), and the Claude/Codex CLIs used by your
profile. Log into those CLIs yourself. The optional design workflow also needs Node.js 20+ and
`playwright-core` with Chromium. See [design setup](docs/design.md).

### From source

```sh
make build                       # Go 1.25+, no third-party Go modules
./dist/agentgate setup install --lead both
./dist/agentgate setup doctor
```

The online installer remains available for published GitHub releases:

```sh
curl -fsSL https://raw.githubusercontent.com/knaus94/agentgate/main/scripts/install.sh | sh
```

A locally delivered archive may be ahead of GitHub. Check versions before running `agentgate update`.

## Start a task

Open a new Claude Code or Codex session after installing the integration:

```text
/dev add rate limiting to the export endpoint          Claude Code
$dev add rate limiting to the export endpoint          Codex

/dev -l rename the export flag                         light: a plan, stages and checks, no other model
/dev -h move invoice totals to integer cents           heavy: a written design and independent readers
/dev review --fix the current branch changes           read an existing change and fix what is found
/dev plan -x split the wallet service                  plan only
/dev --workers -w add pagination to the orders list    sandboxed writers, in an isolated worktree
```

Every task begins with a plan. The session asks you what the code cannot answer, finds the nearest existing
implementation of every part, and writes the plan as small ordered stages; above light a second model that is
shown the conversation reads the plan, and you approve it with its price in front of you — the model runs it
starts besides the session. Then the session writes one stage at a time and `agentgate run` closes each: it
audits the scope, runs the stage's checks and scans the added lines, with no model. The code is read by the
mode's reader at the checkpoints the plan marks and after the last stage. `--preview` shows intended work.
`plan`, `research` and `verify` are read-only. `review` inspects an existing change; `--fix` adds write
authority.

| Step | What happens | Evidence |
|---|---|---|
| Plan | The session clarifies, grounds the change in existing code and writes a typed plan | A plan validated as a whole, every problem named at once; what each stage follows; the price |
| Read the plan (medium and above) | The advisor reads the plan with the conversation; in heavy and ultra independent critics read the session's design | Blockers the session answers; one it rejects goes to you |
| Write | The session writes the open stage and nothing of a later one | Its report, and an audit of every file changed outside the plan |
| Check | Declared commands run by the tool, before any reader | Exit code and log, bound to the tree they ran against |
| Read the code | At a checkpoint and after the last stage: the session itself (light), the advisor (medium), independent lanes (heavy, ultra) | Findings and verdicts bound to content and contract |
| Correct | A blocking finding comes back to the session; it fixes it or disputes it with evidence | The reader's answer in its own session |
| Accept | A stage, and then the task, is accepted in the same run once everything it requires is current | Passing checks, a current reading where one is placed, due requirements verified; a receipt with what was spent |

With `--workers` the same gate runs sandboxed writers: a read-only worker prepares the plan, architects
design, one writer per stream writes and every stage is reviewed. [How it works](docs/how-it-works.md).

A failure before the task began is diagnostic evidence, not permission to ignore later failures. Changing a
file, the plan's contract or required evidence makes stale exactly what it touches. The plan can be amended at any
step without losing the task. [Why it works this way](docs/decisions/README.md).

## Commands organized around the work

| Need | Command |
|---|---|
| See available commands | `agentgate help`, `agentgate help task` |
| Install / diagnose | `agentgate setup install --lead both`, `agentgate setup doctor` |
| Inspect effective configuration | `agentgate config show`, `agentgate config validate` |
| Observe work | `agentgate task watch`, `agentgate task status --all` |
| Export a machine-readable dashboard | `agentgate task watch --json` |
| Stop work | `agentgate task stop` |
| Continue a task after an interruption | `agentgate task run` |
| Reuse a saved plan | `agentgate plan list`, `agentgate task start --plan <name>` |
| Inspect accounts | `agentgate accounts`, `agentgate accounts discover --save` |
| Inspect provider routes | `agentgate modes` |
| Design tools | `agentgate figma --help`, `agentgate layout --help`, `agentgate fixtures --help` |

Existing flat commands remain supported. Every `--help` is read-only, including help for install, stop and
update. Unknown flags, conflicting input sources, malformed JSON and trailing input are rejected.
See [the command reference](docs/cli.md) for exit codes and JSON behavior.

## A dashboard for people and scripts

```sh
agentgate task watch
agentgate task watch --once --no-color
agentgate task watch --json
```

The panel shows tasks, stage durations, worker activity, checks, findings and the limits of every account by
name. Navigate with arrows or `j/k`;
Enter opens details, Escape goes back, `r` refreshes and `?` shows help. It adapts to terminal width and keeps
the selection in view. `NO_COLOR` is respected. Untrusted text cannot inject terminal control sequences.
Pipes, redirected input and `TERM=dumb` produce one snapshot instead of an interactive loop.
[Dashboard guide](docs/watch.md).

## Four modes: the shape of the process

A mode does not pick a stronger model for the writer: the writer is your session. It sets how far the plan
goes and which second look the work gets.

| Mode | The plan | The plan is read by | The code is read by | Model runs besides the session |
|---|---|---|---|---|
| light `-l` | stages and checks; starts at once | nobody | the session itself, with a proof for every requirement | 0 |
| medium `-m` (default) | the code every stage follows, a map of the area; you approve it | the advisor, which is shown the conversation | the advisor, at checkpoints and at the end | 2, and one per checkpoint |
| heavy `-h` | and a written design | the advisor and an independent critic | independent lanes, also at every stage with its own risk | 2, and two lanes per reading |
| ultra `-x` | and an executed proof for what a risky stage completes | the same; the critic designs blind first | four lanes | 3, and four lanes per reading |

With no flag the mode is medium. The plan's assessment never raises it: the tool says what the facts suggest
and you decide. `agentgate modes` prints the effective table, for a task the session writes and for
`--workers`. The readers and the advisor are routes you configure; a Codex-only profile is included. These
are editable starting configurations, not universal performance rankings.
[Mode rules](docs/modes.md) · [Routing policy](docs/model-selection.md).

```sh
agentgate config set advisor fable:xhigh
agentgate config set executor workers        # sandboxed writers by default
agentgate config validate
```

## Accounts, isolation and recovery

Accounts are selected from configured CLI profiles using known quota headroom. Expired quota windows no
longer block an account forever; failed probes do not invent fresh capacity. An account can be kept for the
lead (`accounts reserve <name>`): no worker runs on it. A limit a model has of its own, such as Fable's, is
counted for that model only. Selected accounts use private
worker credential snapshots, and refreshes are reconciled without overwriting a concurrent login.
[Account guide](docs/accounts.md).

Workers run through `bwrap`: the host is read-only, temporary paths are private, and writers receive a
writable checkout. Files between writers are assigned and audited. This is write confinement for trusted
local development, not a complete secrecy boundary against hostile code. Read
[the security model](SECURITY.md) before granting extension tools or running untrusted projects.

With `--workers -w`, the whole task uses its own worktree; a task the session writes works in your checkout. Landing audits the task scope and preserves unrelated
staged changes. Interrupted operations retain their state; `run` retries unfinished streams instead of
starting a replacement task. A task saved by 0.15 is read and continued as it is. [Worktrees](docs/isolation.md) · [Recovery and migration](docs/migration.md).

## Figma, layout measurement and fixtures

The design toolkit exports reusable packages, measures a page against them, and can run a local stand with
recorded GraphQL responses. Exports publish only after completion; content inventories detect missing or
modified files. Missing fixtures are explicit errors unless you deliberately request empty responses.

Figma supports two transports:

- **REST API:** set `FIGMA_ACCESS_TOKEN` with `file_content:read`, or select `--transport api`. Rendering is
  pinned to the loaded file version. Rate limits are reported without silently switching transport.
- **Browser session:** `agentgate figma login`, then `--transport browser`. This preserves the previous
  workflow. It relies on Figma's internal scene graph and is explicitly a compatibility adapter.

No Figma token is written to the exported package. Layout measurement and independent visual acceptance
remain separate. [Complete design workflow](docs/design.md).

## Development and validation

```sh
make check                       # formatting, vet, Go race tests, Node core tests
npm ci --prefix design-kit --ignore-scripts --no-audit --no-fund
node design-kit/node_modules/playwright-core/cli.js install chromium
make design-browser              # actual Chromium workflow scenarios
make package                     # both Linux binaries + source ZIP + checksums
```

Go integration tests use controlled fake provider executables so they can drive whole tasks — writing,
checks, review, correction, amendment and completion — without consuming subscriptions. They do not claim that a live provider
accepted a particular model. [Validation record](docs/validation.md) lists what was actually run for this
archive and any environment limits.

## Documentation

[Commands](docs/cli.md) · [Architecture](docs/architecture.md) · [How it works](docs/how-it-works.md) ·
[Configuration](docs/configuration.md) · [Modes](docs/modes.md) · [Model routing](docs/model-selection.md) ·
[Accounts](docs/accounts.md) · [Codex](docs/codex.md) · [Extensions](docs/extensions.md) ·
[Dashboard](docs/watch.md) · [Stages](docs/stages.md) · [Design](docs/design.md) ·
[Worktrees](docs/isolation.md) · [Migration](docs/migration.md) · [Design decisions](docs/decisions/README.md) ·
[Research decisions](docs/research.md) · [Releasing](docs/releasing.md).

`agentgate guide` and `agentgate reference <page>` print the lead workflow and the lead's pages installed with
this exact binary; `agentgate install` also installs the pages as the skill `agentgate`. License: [MIT](LICENSE).
