# agentgate reference

Lead commands print JSON. Structured inputs are read from `--file <path>` or `--file -` (stdin); commands that
take a plan also accept `--plan <name>`. Exactly one JSON object is accepted; unknown fields are rejected.
Topics: `agentgate reference plan | inputs | design | isolation | config`.

## Commands

| Command | Input | Purpose |
|---|---|---|
| `parse`, `preview` | raw request | parse the `/dev` arguments; show routes without starting anything |
| `prepare` | the request as typed, or `{request, name?, context?, notes?, answers?}` | a read-only worker drafts or revises the plan; the same worker continues on every call |
| `plan save [name]`, `plan list`, `plan show <name>` | plan | keep a plan in `.agentgate/plans/<name>/` with a readable `brief.md` |
| `route` | plan | mode decision, reasons and models; read-only |
| `architecture` | plan | design passes of heavy and ultra |
| `start [--implement]` | plan | open a task and run it |
| `run` | `{visual?, recheck?, reviewer_timeout_s?}` or nothing | every determined step, until a person is needed |
| `answer` | `{stream, answer}` | reply to a blocked writer in its own session, then run on |
| `decide` | see below | the lead's decisions, then run on |
| `amend` | complete plan with `update` | replace the plan of the active task |
| `status [--full]`, `show <part>` | — | the compact picture; `show findings|plan|stage|checks|report|all` |
| `pause` | `{reason, cancelled?}` | park the task; `cancelled: true` drops it for good |
| `resume`, `stop` | — | lift a pause; cancel the running operation |
| `check` | `{argv}` or `--all` | run declared checks; exit code 2 when one fails |
| `visual`, `design`, `consult`, `tell`, `land` | see `reference inputs` | browser verification, UI design, read-only questions, a note to writers, landing an isolated task |
| `watch [--all] [--once] [--json]` | — | the user's panel |

Setup: `doctor`, `install`, `update`, `modes`, `config …`, `accounts …`, `prune`, `version`.

## The answer of every command

```json
{"task": "a8b48740", "action": "implement", "mode": "medium", "step": "decide",
 "stage": {"name": "bets", "index": 3, "of": 4, "accepted": 2},
 "did": ["writers: 1 of 1 streams completed (s3.r1)", "checks: 3 of 3 pass", "review by astra:medium: 2 findings, 1 blocking"],
 "writers": [{"stream": "api", "status": "COMPLETED", "route": "opus:medium", "summary": "…"}],
 "needs": [{"kind": "requirement", "id": "R3", "what": "R3 is partial: …", "resolve": "…"}],
 "checks": {"declared": 3, "passing": 3, "failing": [], "not_run": 0},
 "findings": {"open": 1, "blocking": 0, "notes": 1},
 "actions": ["decide", "run", "amend", "check", "pause", "stop"],
 "next": "Settle `needs`, each as its `resolve` says."}
```

`step`: `write` → `verify` → (`decide`) → next stage … → `done`; `cancelled`. A pause is `paused: {kind,
reason}` on an unchanged step: `user` (only `resume` lifts it, on the user's word), `decision`, `step`.
A refused command answers the same object with `refused: {action, because}` and exit code 1.

## decide

```json
{"findings": [{"id": "<finding id>", "decision": "rejected", "evidence": "Contrary evidence from the code: src/order.ts:42 …",
               "files": [], "basis": "", "duplicate_of": ""}],
 "review": {"summary": "What I read and verified", "findings": [], "quality": {"requirements": [], "rules": []}},
 "requirements": [{"id": "R3", "action": "fix", "note": "The recovery path is not implemented"}],
 "reassessment": {"evidence": "What the new diagnosis found", "changed_approach": "What the writers do differently"},
 "escalate": false, "brief": "A decision or constraint for the next correction", "streams": ["api"]}
```

Give only what you are settling. Decisions: `accepted` (goes to its author), `rejected` (needs contrary
evidence), `deferred`, `needs-approval`. `review` is the lead's own reading in a mode without reviewer
processes; a finding there is
`{id: "", severity: "P1", confidence: "high", category, path, line, evidence, impact, remedy, preserve, boundary_break}`.
`quality` is required when the review packet has one: every requirement `{id, status: verified|partial|missing,
reason, evidence: [{kind: code|check|browser, ref, detail}]}` and every rule `{id, status:
pass|block|not_applicable|unavailable, evidence}`, each id exactly once.

Severities P0–P3. A finding blocks when its severity is one the mode blocks on (P0–P1; heavy and ultra also
P2), its confidence is not low, or it breaks a boundary; and always once it is `accepted` or `needs-approval`.

## prepare

```json
{"request": "-m add rate limiting to the export endpoint", "name": "export-limits",
 "context": ["docs/exports.md"], "notes": "Keep the public response shape", "answers": {"q1": "per account"}}
```

The first call may be the request text itself, as the user typed it, instead of a JSON object. `request`
starts a preparation; later calls may leave it out. `BLOCKED` returns
`questions: [{id, question, options, recommended}]`. With an active task the worker revises that task's plan;
apply the result with `amend`. `fresh: true` abandons the open preparation and explores again.

## run

```json
{"visual": {"url": "http://localhost:4200", "origins": ["http://localhost:4200"]}, "recheck": false, "reviewer_timeout_s": 1800}
```

All optional. `recheck: true` asks for a fresh judgement of unchanged evidence; it bypasses no condition.
When the task waits because a check fails for a cause that may lie outside the code (a stand, a service),
`run` runs the failing checks again; nothing else is repeated.
