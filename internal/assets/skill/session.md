# A task you write yourself

The default: the lead's own session plans and writes, and the tool holds it to the plan. The loop is in
`agentgate guide`. This page has every input and every need of such a task.

## prepare

The request as the user typed it, or `{request, mode?, attachments?, fresh?}`. No model runs. It records the
working tree and answers `{status: "PLAN", executor: "session", mode, instructions, next}`: `instructions` is
what the plan needs in this mode. `{"mode": "heavy"}` alone answers again with that mode's instructions; the
mode the task runs in is the plan's `mode`, else the request's flag, else medium. `attachments` are files the
user gave, anywhere on the machine: they are copied and shown to the readers. `fresh: true` forgets the open
draft and records the tree again. A request with a Figma link also answers with `design_catalogues` and
`design_instructions`.

## The plan

The fields of `agentgate reference plan`, and four of a task the session writes:

```json
{"stages": [{"name": "totals-contract", "goal": "Totals are integer cents in the order contract", "covers": ["R1"],
             "like": ["src/invoices/service.ts"], "checkpoint": true,
             "assessment": {"size": "ordinary", "risks": ["money"], "evidence": "orders/service.ts writes the total"},
             "streams": [{"name": "core", "domain": "backend", "files": ["src/orders/**"]}]}],
 "grounding": {"summary": "how the area works today", "files": [{"path": "src/orders/service.ts", "role": "writes totals", "symbols": []}],
               "callers": [], "invariants": [], "conventions": [], "unknowns": []},
 "approved": "да, делай"}
```

- **`like`** — the existing code the stage follows; every path must exist. **`like_waiver`** — why nothing
  comparable exists: a new pattern, shown to the user in the brief. Medium and above need one of the two on
  every stage. A plan written with top-level `streams` carries them at the top.
- **`checkpoint`** — later stages build on this one: the mode's reader reads everything since the last
  reading before the next stage opens. In heavy and ultra a stage whose own `assessment` names a risk is a
  checkpoint without the mark. Light ignores it. After the last stage the whole change is read in every mode.
- **`grounding`** — the map of the area, required from medium up: at least `summary` and `files`. The readers
  start from it.
- **`approved`** — the user's words approving the plan; `start` refuses a plan above light without it.

`assessment` is optional. It never raises the mode: `plan save` says what the facts suggest, and you tell the
user. In ultra a requirement due at a stage with its own risk needs `method` `check` or `browser`.

`plan save` answers with `model_runs` and `price`: what the plan starts besides you. The same numbers are in
`brief.md`.

## architecture — the plan is read before the work

Medium: `agentgate architecture --plan <name>`. Heavy and ultra, where independent critics read your design:

```json
{"plan": "order-totals", "design": "Boundaries, data and control flow, the contract each stage leaves, what stays untouched, how it is verified",
 "alternatives": ["Store decimals and round on read: every caller would round differently"],
 "risks": [{"id": "rounding", "evidence": "orders/service.ts:88 sums floats", "remedy": "convert at the boundary, sum integers"}]}
```

The answer: `read_by`, `blockers` `[{id, evidence, remedy}]`, `questions` for the user, `agreements`, in ultra
`independent_designs` (made before the critic saw yours), and `input` — the call or the plan entry that comes
next. Answer every blocker:

```json
{"revise": "<receipt id>", "plan": "order-totals",
 "answered": [{"id": "fable/order", "resolved": true, "how": "the contract stage now comes first"},
              {"id": "astra/cache", "resolved": false, "how": "orders are not cached: src/orders/repo.ts:12 reads the table"}]}
```

No model runs: the record is bound to the plan as it is now and gets a new id. A blocker with
`resolved: false` is the user's to decide: give them both arguments and put their decision in the plan,
`architecture.blockers {id: decision}`. The user's answers to `questions` go in `architecture.answers`. With a
changed design, send `design`, `alternatives` and `risks` again in the same call. A plan whose brief, interface
or mode changed after it was read is bound again the same way before `start`.

A plan whose `ui` has `usage` gets its interface from the designer first, in every mode, light included: the
answer adds `ui_design` and `input.ui`.

## start

`agentgate start --plan <name>`. Above light it needs `architecture {id}` with every question answered and
every blocker resolved or decided, and `approved`. `-w` is refused: a task you write works in the checkout.

## run

```json
{"written": {"summary": "Added OrderTotals in src/orders/totals.ts; the service sums integer cents", "deviations": []}}
```

- **`written`** closes the writing of the open stage, or of the fixes a need `fix` asked for. `deviations`:
  where the work departs from the plan or the design, and why. In a correction, `disputes: [{id, evidence}]`
  for a finding that is wrong: its reader answers.
- **`adopt: [paths]`** — files you changed outside the plan join the open stage and are read with it.
- **`disown: [paths]`** — changes outside the plan that are somebody else's: the user's edit, another session.
- `prepared`, `visual`, `recheck` as in `agentgate reference inputs`.

A `run` without `written` at `write` starts nothing: it answers with the open stage again.

## Needs

| `kind` | Settle it with |
|---|---|
| `write` | the stage in `detail` — `stage`, `goal`, `streams`, `like`, `requirements`, `checks`, `grounding` — then `run {written}` |
| `fix` | `detail.failing_checks` (command, the end of its output, its log) and `detail.findings`; then `run {written: {summary, disputes}}` |
| `scope` | `run {adopt}` or `run {disown}` |
| `review` | light, and any mode configured with `review: []`: `show review`, then `decide {review: {summary, findings, quality}}` — the `needs` page has the shape |
| `finding` | a dispute its reader upheld: fix it (`decide {findings: [{id, decision: "accepted", evidence}]}`), or take it to the user |
| `stall` | a new diagnosis: `decide {work: [{stream, note}]}` reopens the stage with it |
| `requirement`, `rule`, `setup`, `scenarios` | the `needs` page |

## advise

```json
{"question": "The total check keeps failing on refunds: is the sign convention in orders/refund.ts what I think it is?", "files": ["src/orders/refund.ts"]}
```

The advisor (`advisor` in the configuration) answers in prose. It is given the question, the patch of the
open position and, when it is a model of your own provider, the digest of this conversation: the user's
messages, what you said, one line per tool call — no tool results. It decides nothing.

## take

`agentgate take`, or `{"from": "<session id>"}` when several sessions hold one. On the user's word, a task
another session of this checkout was writing continues here: its plan, its accepted stages and its open stage
move to this session. Read `show plan` and `show stage` before you write: this conversation has not seen what
the other one read.

## What a mode places where

`agentgate modes` prints the effective table; the `mode` page explains it.
