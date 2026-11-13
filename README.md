# agentgate

[Русский](README.ru.md)

**A quality gate around sandboxed model workers: your Claude Code or Codex session leads, Claude and Codex
workers write and review, and the gate decides when the task is done.**

*Formerly `claude-dev`. The tool can now be led from Codex as well, so the name no longer fit; an existing
install renames itself on `claude-dev update`.*

You type `/dev add rate limiting to the export endpoint` in Claude Code (`$dev …` in Codex). Your session becomes the lead: it
reads the code, writes the brief and splits the work. `agentgate` then runs the writers and reviewers as
separate processes on the models you configured, keeps receipts of everything they did, and refuses to close
the task while a review, a check or a blocking finding is outstanding.

```
/dev [-l|-m|-h|-x] <task>
   │
   ├─ scout      medium: a cheap model maps the code once for the writers and the reviewers
   ├─ design     heavy, ultra: two independent designs in parallel; the lead accepts one
   ├─ write      one sandboxed writer per stream, wave by wave; writers run the declared checks themselves
   ├─ check      the declared checks run first — reviewers are never paid to read code that fails them
   ├─ review     reviewers from two vendors in parallel, each on a different model than the writer
   ├─ correct    failing checks and accepted findings go back to the same writers, in their own sessions
   └─ finish     only with a current review, passing checks and no blocking finding
```

## Why

- **Several subscriptions at once.** Each job goes to the model you choose, and parallel jobs run on different
  pools (Claude, Fable and Codex by default), so no single limit is the bottleneck. When a pool is
  exhausted, a declared alternate takes over and the substitution is reported.
- **The strongest model creates, the others verify.** The author never reviews its own code: reviewers are
  fresh processes on different models, from two vendors in every mode above light.
- **Evidence, not claims.** Reviews and check results are bound to the exact content they saw. Change a file
  and they are stale.
- **Real isolation.** Every worker runs under `bwrap` with a read-only host. A reviewer cannot write; a writer
  can write only the checkout.
- **Cheap conversations.** A blocked writer is answered in its own session. Corrections go to the writer that
  wrote the code, and the reviewer that raised a finding rechecks only the fix.
- **One dial.** Four modes instead of a matrix of options, and the mode is chosen for you.

## Modes

Without a flag the lead records what it found in the code — size, named risks, whether the change can be
undone, uncertainty — and the mode follows from a fixed rule table. A flag always wins.

| | light `-l` | medium `-m` | heavy `-h` | ultra `-x` |
|---|---|---|---|---|
| Chosen when | trivial, ≤3 files, no risk | ordinary work | a named risk, or complex and uncertain | an irreversible risk, or a severe risk in complex work |
| Context | the lead's own | scout `sol:medium` | the accepted design | the accepted design |
| Architect | the lead | the lead | `fable:high` ∥ `astra:high` | `fable:xhigh` ∥ `astra:xhigh`, then cross-critique |
| Backend writer | `sol:medium` | `opus:medium` | `opus:high` | `opus:xhigh` |
| Frontend writer | `sonnet:medium` | `opus:medium` | `astra:high` | `astra:xhigh` |
| Review: architecture | the lead | `fable:medium` | `fable:high` | `fable:high` |
| Review: correctness | the lead | `astra:medium` | `astra:high` | `fable:xhigh` ∥ `astra:xhigh` |
| Review: security | — | — | — | `astra:high` |
| Effort ceiling | `high` | `high` | `xhigh` | `max` |
| Blocks completion | P0–P1 | P0–P1 | P0–P2 | P0–P2 |

Each cell is `model:effort`. Two rules run through the table:

- **The strongest model the mode can afford creates; the other frontier models verify.**
- **Effort is spent where it pays.** A later round continues the session that already read the code — a
  critique and a revision the architect's, a correction the writer's, a recheck the reviewer's — instead of
  reading it again. Medium runs every model at the level its vendor ships as default, heavy one step
  above, ultra two. `max` is never a default: it is reached only when a correction stalls or the lead raises
  one hard stream. A recheck after a fix runs one step lower and only on the lanes with something to verify.

The choices follow public benchmark, price and vendor-guidance data — see
[Why these models](docs/model-selection.md) — and every cell can be [changed](docs/configuration.md). Named
risks: `authorization`, `security`, `money`, `data-integrity`, `concurrency`, `migration`, `public-contract`.

## Install

Linux, x86-64 or arm64. Requires `bwrap` (bubblewrap), `git`, `diff`, and the CLIs you use:
[Claude Code](https://claude.com/claude-code), [Codex CLI](https://github.com/openai/codex), or both.

```sh
curl -fsSL https://raw.githubusercontent.com/knaus94/agentgate/main/scripts/install.sh | sh
```

The script downloads the release binary into `~/.local/bin`, verifies its checksum and asks which sessions
lead the work:

| Choice | Lead | Workers |
|---|---|---|
| `claude` (default) | `/dev` in Claude Code | Claude and Codex models |
| `codex` | `$dev` in Codex | Codex models only |
| `both` | `/dev` and `$dev` | Claude and Codex models |

`agentgate install --lead <choice>` changes it later. [Leading from Codex](docs/codex.md) explains the
Codex side and the Codex-only model table. Then:

```sh
agentgate doctor     # verify the environment
agentgate modes      # see which model does what
```

Start a new session and type `/dev help` (Claude Code) or `$dev help` (Codex).

Update with `agentgate update`. Remove with `agentgate install --uninstall` and delete the binary.
Build from source with `go build ./cmd/agentgate` (Go 1.25+).

## Use

```
/dev fix the pagination off-by-one in the orders list
/dev -h move invoice totals to integer cents
/dev -2 review --fix the changes on this branch
/dev plan -x split the wallet service
/dev status      /dev stop      /dev help
```

`-N` caps how many model processes run at once. `--preview` shows what would run without starting anything.
`plan`, `research` and `verify` are read-only. `review` judges an existing diff; add `--fix` to let it correct.

Watch every task from any terminal in the repository:

```sh
agentgate watch
```

An interactive panel: the tasks of all sessions, the stages each went through with timings, its workers, checks
and findings — and, one `enter` deeper, what a worker is doing right now, command by command.
See [docs/watch.md](docs/watch.md).

Zed users: `agentgate install --zed` adds `agentgate: watch`, `status` and `stop` to Zed's tasks.

The plan of a task is kept in the repository, in `.agentgate/plans/<name>/`: `plan.json` is what runs,
`brief.md` is the same plan for a person to read. `agentgate plan list` shows them; a saved plan can be
launched from a later session.

## Isolated tasks

```
/dev -w add rate limiting to the export endpoint
```

With `-w` a task works in its own git worktree and branch. Your checkout stays clean while it runs, several
sessions can work in one repository on the same files, and the finished change comes in only on your word —
uncommitted by default. If your checkout moved on, the merge and any conflict are handled inside the worktree.
See [docs/isolation.md](docs/isolation.md).

## Several subscriptions

```sh
agentgate accounts discover --save   # pick up every logged-in CLI profile on this machine
agentgate accounts                   # live limits of each account
```

With more than one Claude or Codex subscription, workers are spread over the accounts by remaining quota, an
account that hits a limit rests until it resets, and the model is substituted only after every account refused
it. See [docs/accounts.md](docs/accounts.md).

## Configure

```sh
agentgate config init                               # create ~/.config/agentgate/config.json
agentgate config set models.sol.model gpt-6.1-sol   # point an alias at another model
agentgate config set modes.medium.backend sonnet:high  # change who does what
agentgate config show
```

A repository can carry its own `.agentgate.json`. See [docs/configuration.md](docs/configuration.md).

## Documentation

- [How it works](docs/how-it-works.md) — the gate, the sandbox, how agents talk to each other, how
  model-written extras are filtered
- [Modes](docs/modes.md) — the rule table and what each mode runs
- [Why these models](docs/model-selection.md) — the public data behind the defaults
- [Configuration](docs/configuration.md) — models, pools, fallbacks, per-repository overrides
- [Several subscriptions](docs/accounts.md) — accounts, limits, load spreading, failover
- [Leading from Codex](docs/codex.md) — `$dev`, the Codex-only setup and its model table
- [Extensions](docs/extensions.md) — plug tools into the workers with one file: a code graph, the Nx project graph
- [The panel](docs/watch.md) — `agentgate watch`: tasks, timings, live worker activity
- [Stages](docs/stages.md) — a large specification done and reviewed part by part
- [Design](docs/design.md) — implementing a Figma design and proving the result
- [Isolated tasks](docs/isolation.md) — `-w`: worktrees, landing, catching up, preparing dependencies
- [Releasing](docs/releasing.md) — how versions are published and installed
- `agentgate guide` and `agentgate reference` print what the lead reads

## License

MIT
