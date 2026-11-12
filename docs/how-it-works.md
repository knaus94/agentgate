# How it works

[Русский](ru/how-it-works.md)

## The roles

| Role | Who | Can do |
|---|---|---|
| Lead | your Claude Code session | everything a normal session can: your MCP servers, plugins and skills stay available |
| Scout | a fresh, cheap model process | read the repository once, return a context pack |
| Architect | a fresh model process | read the repository, return a design |
| Writer | a fresh model process, one per stream | edit the files of its stream |
| Reviewer | a fresh model process | read the diff, return findings |

`claude-dev` is the only thing that starts workers. The lead never spawns agents for the task; it calls
`claude-dev` and follows the `next` field of every answer.

## The stages

Every stage exists because skipping it costs more than running it.

| # | Stage | Who | Why it is there |
|---|---|---|---|
| 1 | Brief | the lead | Someone has to decide what is being built; this is the only stage that talks to the user |
| 2 | Scout | one cheap model, once | Otherwise every worker explores the repository by itself, at its own effort. Skipped in light |
| 3 | Design | two top models, independently | Heavy and ultra only. Two designs take the time of one and show where the real decision is. The lead accepts one |
| 4 | Write | one writer per stream | Writers run the declared checks themselves, so the first thing the gate sees is code its author already tested |
| 5 | Check | the declared commands | Runs before any reviewer. Seconds, against minutes and quota: code that fails its checks is not reviewed |
| 6 | Review | models that did not write the code | The independent judgement. Two vendors in every mode above light |
| 7 | Triage | the lead | A finding is a claim, not a fact. The lead verifies each against the code before anyone acts on it |
| 8 | Correct | the same writers, same sessions | Failing checks and accepted findings go back to whoever wrote the code, with its context intact |
| 9 | Finish | the gate | Refuses unless the review is current, the checks pass and nothing blocks |

Stages 5–8 repeat until the task is clean. Two things keep the loop cheap: a recheck runs only the lanes that
have something to verify plus one correctness lane on the delta, one effort step lower; and effort rises only
when a correction demonstrably stalled.

What is deliberately not a stage: a separate test-writing agent (tests belong to the stream that changes the
behaviour), a merge step (writers share one checkout with disjoint files), and a second design round by
default (one optional revision, then the lead decides).

## Where the plan lives

The lead writes the plan as JSON and saves it with `claude-dev plan save <name>`:

```
<repository>/.claude-dev/plans/<name>/plan.json   what the tool runs
<repository>/.claude-dev/plans/<name>/brief.md    the same plan for a person: brief, mode and why, streams with
                                                  their writers and files, checks, the accepted design
```

Every later command takes `--plan <name>`. Saving again replaces both files, so they always show the plan that
will run. The directory is yours to commit or to ignore; the gate never counts it as part of a change.

To have the same plans on every computer, keep the directory in a synced folder:

```sh
claude-dev plan link ~/iCloud/claude-dev    # once per repository and computer; the folder is remembered
```

This moves `.claude-dev` to `<folder>/<repository name>/` and leaves a link in its place. On the second
computer the same command links the checkout to what is already there. `.claude-dev/config.json` — your own
settings for this repository, read after the team's `.claude-dev.json` — travels with it.

Design receipts and the scout's context pack belong to the checkout, not to one conversation
(`~/.local/state/claude-dev/<checkout>/designs`, `…/scout`). A plan designed today can therefore be launched
from a new session tomorrow without paying for the design again; a receipt stays bound to its brief, files and
mode by a fingerprint.

## The gate

A task is a *gate*: one JSON state file per lead session and checkout, under `~/.local/state/claude-dev/`.

```
implementing ──▶ awaiting-review ──▶ reviewed ──▶ complete
                        ▲               │
                        └── correcting ◀┘        held / cancelled from any active phase
```

When the gate opens it stores the content of every owned file as it was before the task, including your
uncommitted edits. From then on:

- A **review** is bound to a digest of the code and of the task contract. Edit a file, or change the brief, and
  the review no longer counts.
- A **check** result is bound to the files it declared as inputs, or to the whole task when it declared none.
  Checks run before reviewers; a failing check sends the task back to its writers without a review.
- **Findings** keep their ids across rounds. A defect that comes back reopens under the same id; it cannot be
  renamed away.
- `finish` succeeds only when the review is current, every declared check has a current passing result, no
  blocking finding is open and every required browser scenario was observed.

A Stop hook keeps the lead from ending its turn on an unfinished gate, unless an operation is still running or
the task is honestly on hold.

Two sessions can work in the same checkout. Each has its own gate; a file can be owned by only one of them.

## The sandbox

Every worker is started through `bwrap`:

- the whole host is mounted read-only;
- `/tmp` and `/run` are private and empty;
- the worker's own lane directory is writable (its logs, its session);
- a writer additionally gets the checkout writable, and the shared board;
- no MCP servers, plugins, user settings or hooks are loaded.

So a reviewer cannot change code even if it tries, and a writer cannot touch anything outside the repository.
File ownership *between* writers is enforced by assignment and audit, not by the filesystem: after each round
the gate compares the checkout with its manifest and reports any change outside the task's files.

The provider's own sandbox is switched off inside `bwrap`: nesting a second one fails on hardened kernels.

## How agents talk

Nobody "calls" anybody. Every channel is either a packet file or a continued session, so every exchange is on
disk and nothing depends on a daemon staying alive.

| Channel | From → to | Mechanism |
|---|---|---|
| Context pack | scout → everyone | one JSON map of the area: files and their roles, callers, invariants, conventions. Gathered once per contract by a cheap model and named in every later packet |
| Assignment | lead → writer | one JSON packet: brief, the stream's files, context, accepted design. Files are named, never copied |
| Report | writer → lead | fixed schema: status, summary, blockers, deviations, handoff |
| Handoff | writer → writer | the contract a stream actually implemented, delivered in the assignment of the streams that come `after` it |
| Board | writers ↔ writers, lead → writers | a shared directory; each writer posts its public contract as soon as it is decided, and reads the others' |
| Answer | lead → blocked writer | the reply is sent into the writer's **own session**; it continues with full context |
| Findings | reviewer → lead → writer | accepted findings go to the writer that owns the file, in its **own session** |
| Failing checks | gate → writer | the command and the end of its output go back to the writers, in their **own sessions**, before any reviewer runs |
| Recheck | lead → reviewer | the reviewer that raised a finding verifies the fix in its **own session**, reading only the delta, one effort step lower; lanes with nothing to verify do not run |
| Cross-critique | architect ↔ architect | ultra only: each receives the other's design and looks for blockers |

The board is read at natural boundaries. It is a note, not an interrupt: a writer cannot preempt another.

### Why sessions are continued

Starting a fresh process means re-reading the repository. Continuing a session means sending one message. The
three places where that matters are answers, corrections and rechecks, and those are exactly the ones that
repeat. Independence is not lost: a reviewer is still a different model from the writer, in a different
process, and a first review is always a fresh context.

## Parallelism and pools

Each model alias belongs to a *pool* — the subscription or quota it draws from. Lanes that run at the same time
are routed to different pools by the mode table, so they do not compete for one limit:

```
heavy scout      sol:medium (pool codex), once
heavy design     fable:high (pool fable)   ∥  astra:high (pool codex)
heavy writers    fable:high (pool fable), one per stream
heavy review     opus:high  (pool claude)  ∥  astra:high (pool codex)
```

With several subscriptions configured, each call first picks the account with the most room for its model,
and an exhausted account hands over to the next one on the same model ([accounts](accounts.md)).
When every account of a provider answers that its limit is exhausted, the lane is retried on the declared alternates in order.
Two rules are never broken: the model that wrote the code is not used to review it, and the substitution is
recorded and shown to the user.

## What is not here

- No shared task list that workers pull from, no long-lived worker panes. Workers are processes with one job.
- No per-writer worktrees. Writers of one task share a directory with disjoint files; nothing has to be merged
  between them. A whole task can be isolated in its own worktree with `-w` ([isolation](isolation.md)).
- No automatic commits, pushes or merges in your checkout. The lead reports; the user decides. An isolated
  task is committed on its own private branch and lands only on the user's word.
