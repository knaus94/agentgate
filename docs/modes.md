# Modes

[Русский](ru/modes.md)

A mode is the shape of the process, not a row of models: how far the questions to the user go, what the plan
must contain, and which kind of second look the work gets, and where. By default the lead's own session
writes the task and the mode sets the process around it; with `--workers` sandboxed writers do the work and
the mode also fixes their models ([decision 0013](decisions/0013-the-session-writes.md)).

## How the mode is chosen when the session writes

A flag in the request (`-l`, `-m`, `-h`, `-x`), or `mode` in the plan. Without either: **medium**. The plan's
`assessment` never raises it. When its facts suggest more, `plan save` says so and the session tells the user:
what a task may spend is the user's decision, and the price of the plan — the model runs it starts besides
the session — is in its brief before they approve.

## What each mode adds

| | light `-l` | medium `-m` | heavy `-h` | ultra `-x` |
|---|---|---|---|---|
| Questions to the user | only those that block | the full grill | the full grill | the full grill, and authorizations for proof |
| Grounding | the nearest existing implementation of the change | one per stage (`like`), and the conventions and invariants of the area | the same, with the callers that constrain the change | the same |
| The plan must hold | stages and checks | requirements as outcomes | the session's design: boundaries, flow, alternatives, risks | an executed proof for every requirement a risky stage completes |
| The plan is read by | nobody | the advisor, with the conversation | the advisor and an independent critic | the same; the critic designs blind first |
| Start | at once | on the user's approval | on approval | on approval |
| The code is read by | the session: its own patch, a proof for every requirement | the advisor | independent lanes: correctness ∥ architecture and simplicity | correctness ×2 ∥ security ∥ architecture and simplicity |
| Where it is read | after the last stage | at checkpoints and after the last stage | the same, and at every stage with its own risk | the same |
| Blocking severities | P0, P1 | P0, P1 | P0, P1, P2 | P0, P1, P2 |
| Model runs besides the session | 0 | `2 + C` | `1 + K + L × (C + 1)` | the same, and a second round per critic |

`C` is the checkpoints before the last stage, `K` the critics, `L` the review lanes of the mode. A round of
fixes adds a recheck of what it changed, in the reader's own session and one effort step lower; a stall adds
a question to the advisor. `agentgate modes` prints the effective table.

- **Light** is a plain session with a seat belt: a plan before code, stages in order, checks run by the tool
  and bound to the files they ran against, a scan of the added lines, a requirement-by-requirement reading
  whose references the tool validates, and a Stop hook that holds an unfinished task. A two-line fix needs none
  of this and should not use `/dev`.
- **Medium** adds a second model that knows the conversation: it challenges the plan, reads the change and
  answers `advise` when a fix goes nowhere.
- **Heavy** adds readers that do not know the conversation — a critic of the design, and review lanes — and
  asks for a written design. It keeps blocking on P2: in the measured tasks 70 of 72 findings were P2, and most
  were real.
- **Ultra** adds a second design made without seeing the session's, and proof that is executed. It is never
  chosen by the tool.

## Stages, checkpoints and risk

A stage is one small outcome that leaves its checks green. Closing it costs no model run: the tool audits the
scope, runs the stage's checks and scans the added lines. A reading is placed at a **checkpoint**: a stage the
plan marks (`"checkpoint": true`) because later stages build on what it leaves — a schema, a contract, a public
interface. There the mode's reader reads everything since the last reading, and the whole change is read after
the last stage. A named risk belongs to the stage that changes the risky behaviour, as that stage's own
`assessment`; in heavy and ultra such a stage is a checkpoint without the mark, and in medium it only tells
the advisor where to look. A risk named for the whole plan places nothing. [Stages](stages.md).

## The advisor and the conversation

`advisor` in the configuration is the route that is shown the conversation. It reads a digest: the user's
messages as they wrote them, what the session said, and one line per tool call. Tool results and reasoning are
left out. The digest goes only to a model of the lead's own provider; a route of another provider reads the
plan and the patch without it. Independent critics and reviewers are never shown it.

```sh
agentgate config set advisor fable:xhigh
agentgate config set session.medium.review '[{"axes":["correctness","architecture","simplicity"],"route":"fable:high","conversation":true}]'
agentgate config set executor workers      # sandboxed writers by default
agentgate modes
```

## With `--workers`: sandboxed writers

A mode fixes everything at once: whether there is a design pass, who writes, who reviews, and which findings
block completion.

## How the mode is chosen with `--workers`

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
| Preparation | `astra:medium` | `astra:medium` | `astra:medium` | `astra:medium` |
| Architects | `opus:medium` | `opus:high` | `opus:high` ∥ `astra:xhigh`, independent, in parallel | `fable:high` ∥ `astra:max`, then each critiques the other one step lower |
| Closing round | — | — | the first architect, in its own session: one design out of two | the same, answering every blocker of the critique |
| Revision | the architect | the architect, on the user's answers and remarks, before the task or during it | the same | the same |
| Backend writer | `sol:high` | `sol:xhigh` | `astra:high` | `astra:xhigh` |
| Frontend writer | `sol:high` | `sol:xhigh` | `astra:high` | `astra:xhigh` |
| Review lanes | one lane, `opus:medium`: correctness, architecture, simplicity | one lane, `opus:high`: correctness, architecture, simplicity | correctness `fable:high` ∥ architecture+simplicity `opus:high` | correctness `fable:xhigh` ∥ correctness `opus:xhigh` ∥ security `opus:high` ∥ architecture+simplicity `fable:high` |
| UI designer | `opus:medium` | `opus:medium` | `opus:high` | `opus:xhigh` |
| Browser check | `astra:high` | `astra:high` | `astra:high` | `astra:high` |
| Effort ceiling | `xhigh` | `xhigh` | `xhigh` | `max` |
| Blocking severities | P0, P1 | P0, P1 | P0, P1, P2 | P0, P1, P2 |
| Declared checks | optional | at least one per stage, or a stated `checks_waiver` | the same | the same |
| Review of the whole change after the last stage | with three or more stages | with three or more stages | always, for a staged plan | always, for a staged plan |
| Process ceilings (design / writer / reviewer) | default 20 min | default 20 min | 30 / 30 / 20 min | 60 / 60 / 30 min |

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

**3. `max` is earned, never assumed.** Only the second architect of ultra starts at it. A writer's effort rises one step, up to the mode's
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

An interface the change adds is planned with `ui.usage`: who uses it and the jobs they come for, each with what
the person has in hand when they begin. Such a plan gets its interface from the mode's UI designer inside the
same `architecture` call, in every mode, before the architects design on it; the designer walks every job
through its interface, and a job that cannot be done is a question to the user, not a detail. `start` refuses
the plan until that design is accepted, and the first review lane of a stage that writes interface code also
reports the axis `usability`: a job the code cannot do blocks, friction is a note.

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
