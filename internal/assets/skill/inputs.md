# Other inputs

## prepare

```json
{"request": "-m add rate limiting to the export endpoint", "name": "export-limits", "mode": "heavy",
 "context": ["docs/exports.md"], "attachments": ["/home/me/Downloads/brief.pdf", "/home/me/shots/limits.png"],
 "notes": "Keep the public response shape", "answers": {"q1": "per account"}}
```

The first call may be the request text itself, as the user typed it, instead of a JSON object. `request`
starts a preparation; later calls may leave it out and continue the same worker. `request` is the user's own
text — for a task that grew out of a conversation, their messages quoted in order. `notes` are yours: the
worker follows a note that quotes the user and checks anything else in it against the code as a hint. `BLOCKED` returns
`questions: [{id, question, options, recommended}]`: ask the user, then send `answers`. `mode` is kept in the
plan, stronger than a flag (the `mode` page). `attachments` are files the user gave, anywhere on the machine:
they are copied, shown to the worker — pictures as pictures — and carried into the task for its writers and
reviewers. With an active task the worker revises that task's plan; apply the result with `amend --plan
<name>`. `fresh: true` abandons the open preparation and explores again.

## run

```json
{"visual": {"url": "http://localhost:4200", "origins": ["http://localhost:4200"]}, "recheck": false, "reviewer_timeout_s": 1800}
```

All optional. `recheck: true` asks for a fresh judgement of unchanged evidence; it bypasses no condition.
When a check fails for a cause that may lie outside the code (a stand, a service), `run` runs the failing
checks again; nothing else is repeated.

## architecture (a mode with architects)

`agentgate architecture --plan <name>`. Each architect reads the code and designs from the plan and its
assessment. With two architects (heavy, ultra) they design independently; in ultra
each then looks for blockers in the other's design; the first architect closes the pass in its own session with
the one design the task follows, having read the other design and answered every blocker. You choose nothing
between designs.

The answer: `lanes` (every round, for the record; the last design is the one followed), `questions` the
architect asks the user, `disputed` (a blocker the architects disagree about), and `input` — the plan entry
that accepts the design:

```json
{"architecture": {"id": "<receipt id>", "answers": {"retention": "30 days"}, "blockers": {"astra/b1": "The user: keep the lock"},
                  "acceptance": "The user also wants the old endpoint kept for one release"}}
```

- `answers`: the user's answer to every question, the option as written or their own words. The design is
  built on the recommended options, so an answer that is the recommendation needs nothing more.
- `blockers`: only for `disputed` — the user's decision on each, after you gave them both arguments.
- `acceptance`, optional: what else the user decided about the design, in their words. Never your own
  amendments.
- Without questions: tell the user the design in a few lines — what it changes, what it rejected
  (`alternatives`) — and go on.

**Back to the architect.** An answer other than the recommended one, and anything the user says about the
design — a doubt, an alternative, a new constraint — is the architect's to work in, before the task and during
it:

```json
{"revise": "<receipt id>", "answers": {"retention": "Keep forever"}, "notes": "The user: «why not a materialized view? will it hold for a popular streamer?»",
 "plan": "export-limits"}
```

`plan` names the saved plan; leave it out during a task, whose plan is used. The architect continues its
session and returns a new receipt: the design as it stands now — changed, or kept with the evidence that it
holds — new `questions` if any, and `plan_changes`: what the plan needs, in the plan's terms. Accept the new
receipt the same way; a running task takes it with
`amend {"update": "…", "architecture": {"id": "…", "answers": {…}}, "requirements": […]}` together with the
plan changes. `start` and `amend` refuse a design whose questions wait, or whose answers differ from what it
was built on.

A receipt is bound to the brief, the UI contract, the mode and the project rules; when one of them changed, a
revision brings the design to the present contract. Adding a requirement or a stage does not unbind it.

## design (new screens and flows)

```json
{"task": "...", "brief": "...", "usage_context": "Roles, frequency, decisions, data", "files": [], "context": [],
 "mode": "heavy", "revise": "<design id>", "notes": "Lead notes for the revision"}
```

`mode` is the task's mode: its `ui_designer` runs. Accept only a `READY` design, as
`ui {design: "<id>", acceptance}` in the plan; at most two revisions. Exact
local edits need no designer: `ui {summary, criteria, preserve}` written by you. Non-visual edits of UI files:
`ui {skip}`.

## consult (research, verify, a diagnosis)

```json
{"lanes": [{"name": "callers", "question": "Who calls X and with what invariants?", "files": ["src/x.ts"], "route": "fable:high"}],
 "timeout_s": 1200}
```

Parallel read-only questions. No task is opened and nothing is edited. `verify` requests run only existing
authorized checks.

## visual (browser verification, opt-in)

```json
{"choice": {"enabled": true, "reason": "User asked", "scenarios": [{"id": "empty", "screen": "Orders", "state": "no orders",
                                                                   "viewport": "1440x900", "role": "manager", "expected": "…"}]}}
{"skip_reason": "Stand cannot start"}
{"url": "http://localhost:4200", "origins": ["http://localhost:4200", "http://localhost:3000"], "login": "", "notes": "",
 "entry_points": [], "storage_state": "/path.json", "timeout_s": 1800, "exclusions": [{"id": "scenario", "reason": "..."}]}
```

A scenario may carry `stage`: it is due at that stage, the last stage when it names none, and every scenario
in the pass over the whole change. A scenario is what a user does — open, click, type, hover, press keys,
resize, wait — and what then shows. Slow or reordered responses, a hidden tab and controlled time are not
scenarios: a test proves them.

The first records the user's choice, the second a skip of the present stage, the third runs a pass by itself. Usually the pass runs
inside verification instead: `run {visual: {url, origins}}`. Only loopback stands are reachable. Starting a
stand needs the user's approval of the exact commands. A layout or type check is not a browser observation.

## check

`{"argv": ["yarn", "nx", "test", "orders"]}` or `agentgate check --all`. Only commands the plan declares; to
add one, `amend {"update": "…", "checks": [{"add": [["yarn", "nx", "test", "orders"]]}]}`. The answer's
`result` holds the receipts; exit code 2 when one failed.

## tell

`{"message": "The response shape is {items, next}"}` appends a note to the board the writers of the task share.
It reaches writers that are running; it is a note, not an interrupt. Work for a writer is `decide {work}`.

## pause

`{"reason": "Waiting for the user's decision on retention", "cancelled": false}`. Any command that moves the
task lifts the pause, except after a user stop.

## land

`{"how": "apply"}`, or `{"how": "commit", "message": "fix(orders): keep totals in cents"}` — the message
follows the repository's convention (read its recent `git log`). See `agentgate reference isolation`.
