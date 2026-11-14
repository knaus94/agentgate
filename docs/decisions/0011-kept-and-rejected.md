# 0011 — What was kept, and what was considered and rejected

## Context

The request was to rethink the tool, using the repository as material rather than as a constraint. The
evidence in [0001](0001-what-0.14-0.15-showed.md) locates the defects: nine of eleven fixes, every dead end
and most of the wasted tokens come from the lifecycle of a task and from the protocol between the tool and
its lead. Nothing in the run logs implicates the process sandbox, the provider adapters, account routing or
the design tools.

## Decision

Rewrite the core of a task. Keep what the evidence does not implicate.

**Rewritten**

| Part | Records |
|---|---|
| Position, pauses, the table of legal actions | [0002](0002-one-state-machine.md) |
| The plan: stages, due requirements, validation of the whole plan with every problem at once | [0003](0003-one-plan-shape.md) |
| Before-images, review and check bindings, scope as paths and patterns | [0004](0004-evidence-on-git-trees.md) |
| `run`, corrections, disputes, stall detection | [0005](0005-autopilot-and-author-filtered-findings.md) |
| Amendment | [0006](0006-amendment-is-ordinary.md) |
| Preparation | [0007](0007-preparation-is-a-conversation.md) |
| Answers to the lead, the guide, the reference, the waiting rules | [0008](0008-lead-protocol.md) |
| Review packets, lanes of the medium mode, reassessment of criteria | [0009](0009-review-economics.md) |
| The state format | [0010](0010-state-format-and-migration.md) |

**Kept, adapted only where they touch the above**

- The sandbox of workers (bubblewrap, read-only reviewers, writers confined to the checkout or worktree).
- The provider adapters for both CLIs, session continuation, alternates on a usage limit, account routing.
- The operation lock, the journal, cancellation and the Stop hook.
- Layered configuration, profiles and the mode table. Modes still fix models and effort; effort is still
  earned by a stalled correction and not chosen by how important a task feels.
- Architecture passes for heavy and ultra, consultations, the UI specification pass, browser verification.
- Requirements with a declared method of proof, mandatory project rules, slop signals.
- Isolated worktrees and landing.
- The design kit: Figma export, layout measurement, fixtures.
- The panel and the installer.

Checks before reviewers, the author never reviewing its own code, content-bound receipts and file ownership
between sessions are the parts the logs showed working, and they are unchanged in substance.

## Considered and rejected

- **A rewrite from nothing.** The sandbox, the adapters and the landing code hold years of small corrections
  that no log line would have asked for again until it broke.
- **Another round of guards on the phases.** [0002](0002-one-state-machine.md).
- **More agents.** The measurements say the opposite: for every token spent writing, nine were spent around
  it. This version removes a role (the scout), a lane (the second reviewer of the medium mode) and four
  commands, and adds none.
- **Parallel writers on shared files.** Published results and the tool's own history agree that writes stay
  single-owner; streams keep disjoint files and waves keep their order.
- **The lead triaging every finding.** [0005](0005-autopilot-and-author-filtered-findings.md).
- **A daemon that runs the task and notifies the lead.** Neither client can be woken from outside; a
  blocking command with a long wait is what both support ([0008](0008-lead-protocol.md)).
- **An MCP server as the lead's interface.** It would give typed inputs. Operations run for minutes, both
  clients already wait for a shell command in a verified way, and a second transport would need the same
  guarantees proved again. It can be revisited when the clients' long-running tool calls are documented.
- **Reviewer lanes chosen by the size of the diff.** A second mechanism beside modes, with thresholds nobody
  could justify; the mode already says how much review a risk deserves.
- **Summaries written by a model at the end.** The writers' reports are the record
  ([0005](0005-autopilot-and-author-filtered-findings.md)).
- **Carrying earlier receipts through the migration.** [0010](0010-state-format-and-migration.md).

## An outside review that was weighed

A written review of the 0.15 code recommended a revision of the lifecycle rather than a rewrite of the tool:
one module of transitions that also feeds the advice to the lead, plan revisions as part of the model,
accounting for generator output, preparation as a dialogue that continues, a typed plan instead of a JSON
string, and a compact matrix of scenario tests. It was read as one input among the measurements, not as a
specification. Where the measurements support it, the result agrees with it; it goes further in three
places the review did not ask for: the lead no longer drives the steps, findings are answered by their
author before anyone rules on them, and the earlier command names are replaced rather than kept.

## Consequences

- The surface a user configures (modes, routes, accounts, worktree, design) is the same; the surface a lead
  drives is smaller and different, and installed lead commands must be reinstalled (`agentgate install`).
- What was kept was adapted by hand and is covered by the same suites as before. What was rewritten is
  covered by scenario tests that drive whole tasks through the public commands with scripted workers; every
  answer in those scenarios is checked against the table of legal actions.
- Five live scenarios on Codex models ran before the first commit of this version
  ([validation](../validation.md)). They found six defects that scenarios with scripted workers had not:
  every one of them was a place where a real model's reasonable answer met a rule written for an imagined
  one. Heavy and ultra, isolated worktrees, Claude workers and an interactive lead session were not run
  live; the first real tasks there are the validation that remains.
