# Other inputs

## prepare

```json
{"request": "-m add rate limiting to the export endpoint", "name": "export-limits", "mode": "heavy",
 "context": ["docs/exports.md"], "attachments": ["/home/me/Downloads/brief.pdf", "/home/me/shots/limits.png"],
 "notes": "Keep the public response shape", "answers": {"q1": "per account"}}
```

The first call may be the request text itself, as the user typed it, instead of a JSON object. `request`
starts a preparation; later calls may leave it out and continue the same worker. `BLOCKED` returns
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

## architecture (heavy, ultra)

`agentgate architecture --plan <name>`. Two architects read the code and design independently; in ultra each
then looks for blockers in the other's design, in its own session. The answer is a receipt with the designs
and blockers. You decide: record in the plan

```json
{"architecture": {"id": "<receipt id>", "base": "fable", "acceptance": "Your amendments to that design",
                  "blockers": {"astra/b1": "Resolved by ..."}}}
```

`base` is the alias of the design the task follows (default: the first). Writers and reviewers receive that
one design as amended by `acceptance`, never both, so the acceptance must be complete enough to follow. Answer
every blocker in `blockers`: how it is resolved, or why it is wrong. One revision round exists for blockers you
verified against the code: add `revise: "<receipt id>"` and `revise_blockers: ["id"]` to the same plan and run
`architecture` again. A receipt is bound to the brief, the UI contract, the mode and the project rules;
changing one of them needs a new pass. Adding a requirement or a stage does not.

## design (new screens and flows)

```json
{"task": "...", "brief": "...", "usage_context": "Roles, frequency, decisions, data", "files": [], "context": [],
 "effort": "medium", "revise": "<design id>", "notes": "Lead notes for the revision"}
```

Accept only a `READY` design, as `ui {design: "<id>", acceptance}` in the plan; at most two revisions. Exact
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

The first records the user's choice, the second a skip, the third runs a pass by itself. Usually the pass runs
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
