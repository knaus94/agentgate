# 0006 — Amending the plan is an ordinary operation with scoped invalidation

## Context

A large specification is not fully known when the work starts. In the measured task the plan was changed
after the first writer began: the generator chose a migration path nobody could have named, the coverage of
six requirements was wrong, and intermediate deliverables needed their own criteria. The tool offered four
partly overlapping ways to change a plan — `launch` with `update` while implementing or between stages,
`replan` when reviewed, `continue` after an interruption, `answer` for a blocked writer — and each was legal
in different phases. The journal shows the lead trying them in turn.

Any change also invalidated the code review: the review was bound to one digest of the brief, the files,
the streams, the checks and the requirements together. Moving a requirement from one stage to another
caused a full review of unchanged code, three times in one afternoon.

## Decision

One command, `amend`, takes the complete revised plan and a reason. It is legal whenever the task is active
and no operation is running, at any step.

**What may change**

| Part of the plan | Rule |
|---|---|
| Accepted stages | Frozen: name, streams, coverage, checks |
| Requirements covered by an accepted stage | Frozen: id, text, method |
| Other requirements | May be added freely. Changing or removing one needs `authorized`: who allowed it and in what words. A requirement is never weakened to pass a review |
| The current stage | Goal, acceptance, coverage and checks may change. A stream that already has a writer keeps its name. A path the stage already changed may not leave its scope. Streams may be added |
| Later stages | Free, as long as every requirement stays covered |

**What it invalidates** — only what the change touches:

| Changed | Effect |
|---|---|
| Later stages only | Nothing |
| Coverage, requirements, checks of the current stage | The requirement verdicts are reassessed by the reviewer in its own session; the code review stands |
| Brief, goal, acceptance, scope, UI, design, architecture of the current stage | The code review is no longer current |
| Streams named in `rework`, and every added stream | They get the amendment as a new assignment and the task returns to `write` |

To make this possible a review is bound to two digests instead of one: the *code contract* (what was to be
built) and the *criteria* (what is judged and with which receipts).

**What it records.** Every plan the task ran under is kept as a revision: number, time, reason, digest and
a computed list of what changed (requirements added or moved, stages added or removed, scope widened). The
saved brief shows the list. A new session can restart from the plan and its revisions alone.

An architecture receipt is bound to the brief, the UI contract, the mode and the project rules, and no
longer to the requirement list or the stage names: adding an intermediate criterion does not discard a
design pass.

## Alternatives

- **Deltas as the input** (added / modified / removed, as OpenSpec writes changes). A delta is the right
  *record* and is what the revision stores. As *input* it would make the preparer and the lead compute a
  patch against a plan they hold whole; the tool computes it more reliably.
- **A new task for a changed plan.** It discards sessions, findings and the baseline — the things an
  amendment exists to keep.
- **Let any amendment through and re-review everything.** That was the behaviour; it priced a correction of
  metadata as a review of code.

## Consequences

- `replan`, `launch` with `update` and the "keeps its shape" rule are gone.
- A reviewer's correct `partial` now has a cheap legal answer: move the coverage, add the intermediate
  criterion, reassess.
- The reviewer's session must be continuable for the cheap reassessment. When it is not (light mode, an
  expired session, a model that is no longer permitted), the reassessment is a fresh review.
