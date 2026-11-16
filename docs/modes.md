# Modes

[Русский](ru/modes.md)

A mode fixes everything at once: whether there is a design pass, who writes, who reviews, and which findings
block completion. There is no second dial.

## How the mode is chosen

A flag in the request is final: `-l`, `-m`, `-h`, `-x` (or `--light`, `--medium`, `--heavy`, `--ultra`).

Without a flag the plan carries an `assessment` of what was found in the code — the preparation worker writes
it with its evidence — and the mode follows from this table, first match wins:

| # | Facts | Mode |
|---|---|---|
| 1 | a named risk **and** the change cannot be undone once shipped | ultra |
| 2 | a severe risk (`authorization`, `security`, `money`) **and** the change is complex | ultra |
| 3 | any named risk | heavy |
| 4 | complex **and** materially uncertain | heavy |
| 5 | trivial, certain, at most 3 files, one stream | light |
| 6 | everything else | medium |

The assessment has four facts and the evidence for them:

```json
{"size": "ordinary", "risks": ["data-integrity"], "irreversible": false, "uncertainty": "low",
 "evidence": "Totals are written in one transaction in orders/service.ts; two callers"}
```

Named risks: `authorization`, `security`, `money`, `data-integrity`, `concurrency`, `migration`,
`public-contract`. A risk is named only when the change really touches it. "This module has many callers" is
not a risk; it is ordinary work, and ordinary work is medium.

`agentgate route --file plan.json` prints the decision with its reasons before anything starts. In a plan
of several stages each stage may carry its own assessment and gets its own mode.

## What each mode runs

| | light | medium | heavy | ultra |
|---|---|---|---|---|
| Preparation | `sol:medium` | `sol:medium` | `sol:medium` | `sol:medium` |
| Architects | the plan | `opus:high` | `fable:high` ∥ `astra:high`, independent, in parallel | `fable:xhigh` ∥ `astra:xhigh`, then each critiques the other at `high` |
| Closing round | — | — | the first architect, in its own session: one design out of two | the same, answering every blocker of the critique |
| Revision | `prepare` with notes | the architect, on the user's answers and remarks, before the task or during it | the same | the same |
| Backend writer | `sol:high` | `sol:xhigh` | `opus:high` | `opus:xhigh` |
| Frontend writer | `sol:high` | `sol:xhigh` | `astra:high` | `astra:xhigh` |
| Review lanes | one lane, `astra:medium`: correctness, architecture, simplicity | one lane, `opus:high`: correctness, architecture, simplicity | architecture+simplicity `fable:high` ∥ correctness `opus:high` | architecture+simplicity `fable:high` ∥ correctness `fable:xhigh` ∥ correctness `opus:xhigh` ∥ security `opus:high` |
| UI designer | `astra:medium` | `astra:medium` | `astra:high` | `astra:xhigh` |
| Browser check | `astra:high` | `astra:high` | `astra:high` | `astra:high` |
| Effort ceiling | `xhigh` | `xhigh` | `xhigh` | `max` |
| Blocking severities | P0, P1 | P0, P1 | P0, P1, P2 | P0, P1, P2 |
| Declared checks | optional | at least one per stage, or a stated `checks_waiver` | the same | the same |
| Review of the whole change after the last stage | with three or more stages | with three or more stages | always, for a staged plan | always, for a staged plan |
| Process ceilings (design / writer / reviewer) | default 20 min | default 20 min | 15 / 30 / 20 min | 30 / 60 / 30 min |

The preparation route is the key `modes.medium.scout`; the key keeps the name it had when a separate scout
existed. A mode may set its own.

Medium has one review lane where it had two. In the measured runs the second lane read the same change at
the same price and its findings overlapped; the mode that pays for two vendors' opinions is heavy
([decision 0009](decisions/0009-review-economics.md)). `modes.medium.review` restores a second lane.

The ladder has one rule for models: **the strongest model the mode can afford creates; the other frontier
models verify.** [Why these models](model-selection.md) shows the data.

## How effort is spent

Reasoning effort is the main cost dial, and usage limits are finite. Four rules keep it where it pays.

**1. Code is read once, and a later round continues that reading.** An expensive model opens the files itself
whatever map it is handed: in a measured design pass the architects read the same files with a prepared map
and without one, and the map only made them wait. So architects get none. In heavy and ultra the accepted
design — made by the strongest models after reading the code — is what writers and reviewers start from. In
every mode the worker that prepared the plan hands over its map of the area: which files matter and why, who
calls them, the invariants, the conventions. Nobody is paid to explore the same area a second time, and no
worker starts over: an answer continues the preparation worker's session, a critique and a revision the
architect's, a correction the writer's, a recheck the reviewer's.

**2. The base effort follows the vendors' own defaults.** Medium runs each model at the level its vendor ships
as default. Heavy is one step above, ultra two. Effort goes to design and correctness review first — they
produce little text and decide a lot — and less to lanes that read more than they reason.

**3. `max` is earned, never assumed.** No default uses it. A writer's effort rises one step, up to the mode's
ceiling, only when a correction made no verified progress and the lead gave a new diagnosis, when the lead
passes `escalate` to `decide`, or when the plan raises one hard stream with `effort`. Only ultra can reach `max`.

**4. A recheck costs less than a review.** After a fix, only the lanes with something to verify run again, plus
one correctness lane that reads the delta for regressions — each in its own session, one effort step lower. The critique round of ultra does the same against
the design round.

In every mode: a finding at a blocking severity goes to the writer that owns the file, which fixes it or
disputes it with evidence; findings below that line are notes that need no judgement; a boundary break blocks
regardless of severity; a finding the lead accepted blocks until it is resolved.

## Why the design pass is parallel

Design followed by critique is two long model calls in a row. Two independent designs take the time of one.
Ultra adds the critique as a second parallel round, because there the cost of a missed blocker is higher than
the minutes. The first architect then closes the pass in its own session: it reads the other design, answers
every blocker and returns the one design the task follows. Nobody chooses between two texts.

## Who owns the design

The lead coordinates; it does not design. A design asks the user what is theirs to decide as structured
questions with a recommendation, and is built on the recommended options; `start` waits for the answers, and
another answer goes back to the architect. What the user says about the design later — a doubt, an alternative,
a new constraint — goes to the same architect's session, also in the middle of a task: it returns the design as
it stands, or the evidence that it already holds, and what the plan needs. The plan's `assessment` tells the
architect the named risks and, for a path whose cost grows with data, the `load` it has to carry; with `load`
the review lane that judges correctness also reports the axis `performance`.

Design reports are asked to stay near 800 words, and every mode sets its own process ceilings
(`architecture_timeout_s`, `writer_timeout_s`, `reviewer_timeout_s`): generous in ultra, where `max` effort
means long turns, tight elsewhere so a pass cannot quietly take half an hour.

## Changing a mode

```sh
agentgate config set modes.medium.review '[{"axes":["correctness","architecture"],"route":"fable:high"}]'
agentgate config set modes.heavy.blocking '["P0","P1"]'
agentgate modes
```

Leave `review` empty (`[]`) and the lead reviews the patch itself; light has its own reviewer since 0.17.
