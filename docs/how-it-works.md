# How it works

[Русский](ru/how-it-works.md)

## The roles

| Role | Who | Can do |
|---|---|---|
| Lead | your Claude Code or Codex session | everything a normal session can: your MCP servers, plugins and skills stay available |
| Scout | a fresh, cheap model process | medium: read the repository once, return a context pack for the writers and the reviewers |
| Architect | a fresh model process | read the repository, return a design |
| Writer | a fresh model process, one per stream | edit the files of its stream |
| Reviewer | a fresh model process | read the diff, return findings |

`agentgate` is the only thing that starts workers. The lead never spawns agents for the task; it calls
`agentgate` and follows the `next` field of every answer.

## The stages

Every stage exists because skipping it costs more than running it.

| # | Stage | Who | Why it is there |
|---|---|---|---|
| 1 | Preparation | read-only worker, then lead | The worker discovers the repository and drafts the plan; the lead handles product decisions and authorization |
| 2 | Scout | one cheap model, once | Medium only: nobody designs there, so writers and reviewers start from its map. Architects read the code themselves, and in heavy and ultra the accepted design is the map |
| 3 | Design | two top models, independently | Heavy and ultra only. Two designs take the time of one and show where the real decision is. The lead accepts one. A critique (ultra) and a revision continue the architect's own session: the code is already read |
| 4 | Write | one writer per stream | Writers run the declared checks themselves, so the first thing the gate sees is code its author already tested |
| 5 | Check | the declared commands | Runs before any reviewer. Seconds, against minutes and quota: code that fails its checks is not reviewed |
| 6 | Review | models that did not write the code | The independent judgement. Two vendors in every mode above light |
| 7 | Triage | the lead | A finding is a claim, not a fact. The lead assesses the reviewer's cited evidence and sends technical uncertainty back to a specialist |
| 8 | Correct | the same writers, same sessions | Failing checks and accepted findings go back to whoever wrote the code, with its context intact |
| 9 | Finish | the gate | Refuses unless the review is current, the checks pass and nothing blocks |

Stages 5–8 repeat until the task is clean. Two things keep the loop cheap: a recheck runs only the lanes that
have something to verify plus one correctness lane on the delta, one effort step lower; and effort rises only
when a correction demonstrably stalled.

What is deliberately not a stage: a separate test-writing agent (tests belong to the stream that changes the
behaviour), a merge step (writers share one checkout with disjoint files), and a second design round by
default (one optional revision, then the lead decides).

## Where the plan lives

The lead writes the plan as JSON and saves it with `agentgate plan save <name>`:

```
<repository>/.agentgate/plans/<name>/plan.json   what the tool runs
<repository>/.agentgate/plans/<name>/brief.md    the same plan for a person: brief, mode and why, streams with
                                                  their writers and files, checks, the accepted design
```

Every later command takes `--plan <name>`. Saving again replaces both files, so they always show the plan that
will run. The directory is yours to commit or to ignore; the gate never counts it as part of a change.

To have the same plans on every computer, keep the directory in a synced folder:

```sh
agentgate plan link ~/iCloud/agentgate    # once per repository and computer; the folder is remembered
```

This moves `.agentgate` to `<folder>/<repository name>/` and leaves a link in its place. On the second
computer the same command links the checkout to what is already there. `.agentgate/config.json` — your own
settings for this repository, read after the team's `.agentgate.json` — travels with it.

Design receipts and the scout's context pack belong to the checkout, not to one conversation
(`~/.local/state/agentgate/<checkout>/designs`, `…/scout`). A plan designed today can therefore be launched
from a new session tomorrow without paying for the design again; a receipt stays bound to its brief, files and
mode by a fingerprint.

## Keeping model-written code lean

Code written by a model tends to carry extras that look diligent and cost maintenance: comments that narrate
the code, checks for states that cannot happen, a wrapper around one call, a leftover `console.log`. Three
layers deal with it, cheapest first.

1. **The writer is told what not to add**, with the reason: every added line is read now and maintained later.
   Scope, comments, defensive code, abstractions, type escapes, leftovers and "results that are not real" each
   have a rule, and the writer reads its own diff as a reviewer before reporting.
2. **A scan of the added lines, before any reviewer runs.** It costs no model call. It looks only at lines the
   task added, in code files, for literal leftovers: placeholders, TODOs, debug output, disabled tests, type and
   lint suppressions, swallowed errors, comments that narrate the change, conflict markers. The hits are
   `slop_signals`: leads for the reviewers and the lead, never verdicts — the same text can be right.
3. **The `simplicity` axis of the review**, judged in the same process as the design, so it costs no extra
   lane. The reviewer asks of each added piece whether the task would be done, and the code just as safe,
   without it — and must prove it: quote the line and name the fact that makes it unneeded (its only caller,
   the type that rules the state out, the helper that already exists). The remedy is a deletion. Faked results
   and hidden failures are P1; code someone must keep for no benefit is P2. Naming and formatting taste, what a
   linter already reports and code the change did not add are out of scope.

In light mode there is no reviewer process: the lead receives the signals with the diffs and applies the same
test. `/dev clean` remains the way to simplify code that is already in the repository.

The rules draw on public work: the community `/deslop` commands and the `code-slop` skill for the list of
patterns, the Karpathy-inspired guidelines for "every changed line traces to the request", Anthropic's
prompting guidance on over-engineering, and PhotoStructure's review rule "prove it or discard it" for keeping
the axis from turning into a style campaign.

## The gate

A task is a *gate*: one JSON state file per lead session and checkout, under `~/.local/state/agentgate/`.

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
| Context pack | scout → writers, reviewers | one JSON map of the area: files and their roles, callers, invariants, conventions. Gathered once per contract by a cheap model, in the modes that have a scout, and named in every later packet |
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
heavy design     fable:high (pool fable)   ∥  astra:high (pool codex)
heavy writers    opus:high  (pool claude), one per stream
heavy review     fable:high (pool fable)   ∥  astra:high (pool codex)
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
