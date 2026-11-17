# AgentGate

[Русский](README.ru.md) · [Quick start](#quick-start) · [Commands](docs/cli.md) · [Architecture](docs/architecture.md) · [Release notes](RELEASE_NOTES.md)

**A local orchestrator and quality gate for Claude Code and Codex workers.** Your lead session owns the task;
AgentGate runs scoped workers, records their results, runs checks and independent reviews, and allows
completion only while that evidence still matches the change.

AgentGate is a Linux CLI with a live terminal dashboard. It runs without a background daemon or a hosted
service. It uses the provider CLIs and accounts you configure. Former installations named `claude-dev` are
recognized for migration.

## Quick start

### From the complete release archive

The archive includes source, documentation, SHA-256 checksums, and binaries for Linux x86-64 and arm64.
After extracting it:

```sh
cd agentgate-0.21.4
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

/dev -h move invoice totals to integer cents           explicit heavy mode
/dev -w add pagination to the orders list              isolated task worktree
/dev -2 review --fix the current branch changes        at most two workers
/dev plan -x split the wallet service                  plan before implementation
```

A read-only worker prepares the plan and the tool validates all of it before anything starts. From then on
one command, `agentgate run`, takes every step the state determines and stops only where a person is needed:
a blocked writer, a finding its author and the reviewer disagree about, an unmet requirement, a correction
that went nowhere. The lead coordinates and does not design: how the change is built is the architect's, the
user approves it, and what the user says about the design later goes back to the same architect, also in the
middle of a task. `--preview` shows intended work. `plan`, `research` and `verify` are read-only. `review`
inspects an existing change; `--fix` adds write authority.

| Step | What happens | Evidence |
|---|---|---|
| Prepare | One resumable read-only worker explores, asks, and drafts a typed plan | A plan validated as a whole, every problem named at once |
| Design (medium and above) | An architect designs the change and asks the user what is theirs to decide; two architects are closed into one design | The design the task follows, with the user's answers |
| Write | Disjoint streams, dependency waves and bounded parallelism | Structured reports and a file ownership audit |
| Check | Declared commands run before any reviewer | Exit code and log, bound to the tree they ran against |
| Review | A separate model reads one patch and judges the criteria | Findings and verdicts bound to content and contract |
| Correct | A blocking finding goes to the writer that owns the file; it fixes it or disputes it with evidence | The reviewer's answer in its own session |
| Accept | A stage, and then the task, is accepted in the same run once everything it requires is current | Passing checks, a current review, due requirements verified |

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

## Four modes, configurable models

| Mode | Typical scope | Architecture | Review | Blocking severities |
|---|---|---|---|---|
| light `-l` | Small, low-risk change | One architect, `opus:medium` | One reviewer, `opus:medium`: correctness, architecture, simplicity | P0–P1 |
| medium `-m` | Ordinary implementation | One architect, `opus:high` | One reviewer, `opus:high`: correctness, architecture, simplicity | P0–P1 |
| heavy `-h` | Named risk or complex uncertain work | Two independent proposals, closed into one design | Separate architecture and correctness lanes | P0–P2 |
| ultra `-x` | Severe or irreversible risk | Proposals, cross-critique, one closing design | Additional correctness and security lanes | P0–P2 |

With no flag, the recorded assessment selects a mode deterministically. A flag, or `mode` in the plan, takes precedence. Claude models design and
review, Codex models plan, write and check the browser; when the Codex limit runs out, Opus takes the call. A
Codex-only profile is also included. These are editable starting configurations, not universal performance rankings.
[Routing policy](docs/model-selection.md) · [Mode rules](docs/modes.md).

```sh
agentgate config set modes.medium.backend sol:high
agentgate config set modes.medium.frontend sol:high
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

With `-w`, the whole task uses its own worktree. Landing audits the task scope and preserves unrelated
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
