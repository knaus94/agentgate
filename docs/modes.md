# Modes

[Русский](ru/modes.md)

A mode fixes everything at once: whether there is a design pass, who writes, who reviews, and which findings
block completion. There is no second dial.

## How the mode is chosen

A flag in the request is final: `-l`, `-m`, `-h`, `-x` (or `--light`, `--medium`, `--heavy`, `--ultra`).

Without a flag the lead records an `assessment` of what it found in the code, and the mode follows from this
table, first match wins:

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

`claude-dev route --file plan.json` prints the decision with its reasons before anything starts.

## What each mode runs

| | light | medium | heavy | ultra |
|---|---|---|---|---|
| Context pack | the lead's own context | scout `sol:medium` | scout `sol:medium` | scout `sol:high` |
| Architects | the lead | the lead | `fable:high` ∥ `astra:high`, independent, in parallel | `fable:xhigh` ∥ `astra:xhigh`, then each critiques the other at `high` |
| Blocker round | — | — | optional, once | optional, once |
| Backend writer | `sol:medium` | `opus:medium` | `fable:high` | `fable:xhigh` |
| Frontend writer | `sonnet:medium` | `opus:medium` | `fable:high` | `fable:xhigh` |
| Review lanes | the lead | architecture+simplicity `fable:medium` ∥ correctness `astra:medium` | architecture+simplicity `opus:high` ∥ correctness `astra:high` | architecture+simplicity `opus:high` ∥ correctness `opus:xhigh` ∥ correctness `astra:xhigh` ∥ security `astra:high` |
| Effort ceiling | `high` | `high` | `xhigh` | `max` |
| Blocking severities | P0, P1 | P0, P1 | P0, P1, P2 | P0, P1, P2 |
| Checks required to finish | optional | at least one | at least one | at least one |
| `architecture_evidence` at finish | — | — | required | required |
| Process ceilings (design / writer / reviewer) | default 20 min | default 20 min | 15 / 30 / 20 min | 30 / 60 / 30 min |

The ladder has one rule for models: **the strongest model the mode can afford creates; the other frontier
models verify.** [Why these models](model-selection.md) shows the data.

## How effort is spent

Reasoning effort is the main cost dial, and usage limits are finite. Four rules keep it where it pays.

**1. A scout reads so the others do not have to.** Without it, every worker explores the repository on its
own — in ultra that would be two architects, the writers and four reviewers, each at high effort. The scout is
one cheap, fast process that maps the area once: which files matter and why, who calls them, the invariants,
the conventions. Every later worker starts from that map and verifies what it relies on. The pack is keyed by
the contract, so the design pass and the writers share one.

**2. The base effort follows the vendors' own defaults.** Medium runs each model at the level its vendor ships
as default. Heavy is one step above, ultra two. Effort goes to design and correctness review first — they
produce little text and decide a lot — and less to lanes that read more than they reason.

**3. `max` is earned, never assumed.** No default uses it. A writer's effort rises one step, up to the mode's
ceiling, only when a correction made no verified progress, when the lead passes `escalate` to `fix`, or when
the plan raises one hard stream with `effort`. Only ultra can reach `max`.

**4. A recheck costs less than a review.** After a fix, only the lanes with something to verify run again, plus
one correctness lane that reads the delta for regressions — each in its own session, one effort step lower. The critique round of ultra does the same against
the design round.

In every mode: P3 findings are notes that need no triage; a boundary break blocks regardless of severity; a
finding the lead accepted blocks until it is resolved.

## Why the design pass is parallel

Design followed by critique is two long model calls in a row. Two independent designs take the time of one, and
their disagreements show the lead where the real decision is. Ultra adds the critique as a second parallel
round, because there the cost of a missed blocker is higher than the minutes.

Design reports are asked to stay near 800 words, and every mode sets its own process ceilings
(`architecture_timeout_s`, `writer_timeout_s`, `reviewer_timeout_s`): generous in ultra, where `max` effort
means long turns, tight elsewhere so a pass cannot quietly take half an hour.

## Changing a mode

```sh
claude-dev config set modes.medium.review '[{"axes":["correctness","architecture"],"route":"fable:high"}]'
claude-dev config set modes.heavy.blocking '["P0","P1"]'
claude-dev modes
```

Leave `review` empty (`[]`) and the lead reviews the diff itself, as in light.
