# agentgate reference

Lead protocol commands print JSON. Help, guides, saved-plan briefs and human-readable tables print text.
Structured inputs are read from `--file <path>` or `--file -` (stdin); commands expecting a plan also accept
`--plan <name>`. Exactly one JSON object is required; unknown fields and trailing values are rejected.
`parse` and `preview` instead read raw request text. Use `agentgate help <command>` for command syntax.

## Commands

| Command | Input | Purpose |
|---|---|---|
| `parse` | raw request | parse the `/dev` arguments |
| `preview` | raw request | show mode handling and routes; no files, no models |
| `plan save [name]` | plan | validate and keep the plan in `.agentgate/plans/<name>/` with a readable `brief.md` |
| `plan list`, `plan show <name>` | — | saved plans of this repository; print a brief |
| `plan link [folder]` | — | user's setup: keep `.agentgate` in a synced folder and link it here |
| `route` | plan | mode decision, reasons and models; read-only |
| `architecture` | plan | design passes of heavy and ultra |
| `design` | design input | read-only UI designer |
| `consult` | consult input | parallel read-only questions (plan, research, verify) |
| `launch` | plan | open or continue the gate and run the writers |
| `answer` | `{stream, answer}` | reply to a blocked writer in its own session |
| `tell` | `{message}` | leave a note on the writers' shared board |
| `check` | `{argv}` or `--all` | run declared checks; exit code 2 when one fails |
| `review` | optional review input | declared checks, a scan of the added lines (`slop_signals`), then review lanes and the optional browser pass; can finish |
| `visual` | visual input | browser pass, or record the choice / a skip |
| `triage` | `{dispositions}` | judge findings |
| `fix` | `{brief, reassessment?, escalate?, streams?}` | send accepted findings and failing checks back to the writers |
| `finish` | `{summary, ...}` | close the task |
| `land` | `{how?}` | isolated task only: bring it into the checkout — `apply` (default), `commit`, `branch` or `drop` |
| `hold` | `{reason, cancelled?}` | park the task |
| `resume`, `continue`, `stop`, `status` | — | resume a hold, retry unfinished streams, cancel and observe |
| `prepare` | `{request, name?, context?, notes?, refresh_designs?}` | read-only worker drafts and saves the plan; reuses an active task's selected packages unless refresh_designs is true |
| `replan` | complete plan with `update` | replace an idle single-pass task contract while preserving its original baseline |
| `watch [--all] [--once] [--json] [--no-color]` | — | the user's interactive panel; `--once` prints text, `--json` a structured snapshot |

Setup: `doctor`, `install [--lead claude|codex|both] [--zed] [--uninstall]`, `update [--check]`, `modes`,
`config show|path|init|validate|set <key> <value>|unset <key>|extensions|profiles|profile <name>`, `prune`, `version`,
`accounts [--json] | discover [--save] | add <name> <claude|codex> <home> [--paid] | remove <name> | reset <name>`
(several subscriptions: limits, load spreading and failover are automatic).

## Request

`[-N] [-l|-m|-h|-x] [-w] [--staged|--single] [--step] [plan|research|fix|review|clean|verify|status|stop|help] [--fix] [--grill|--no-grill] [--preview] <task>`

`--staged` demands a plan in stages; `--single` allows one pass past the size limits; `--step` holds the task
after every accepted stage until the user says to go on.

`-w` (`--worktree`) isolates a writing task in its own git worktree; the checkout changes only at `land`.

`-N` caps how many model processes run at once (1–20). `--light --medium --heavy --ultra` are the long forms;
`--deep` and `--critical` are accepted as heavy and ultra. `--` ends the flags.

## Plan

```json
{
  "request": "<the raw /dev arguments>",
  "assessment": {
    "size": "ordinary",
    "risks": ["data-integrity"],
    "irreversible": false,
    "uncertainty": "low",
    "evidence": "What you inspected and what remains uncertain"
  },
  "brief": "Requirements, non-goals, invariants, acceptance criteria",
  "requirements": [{"id": "R1", "text": "Orders preserve their totals", "method": "check"}],
  "streams": [
    {"name": "api", "domain": "backend", "files": ["src/order.ts"], "brief": "optional stream detail"},
    {"name": "web", "domain": "frontend", "files": ["app/Order.tsx"], "after": ["api"], "effort": "high"}
  ],
  "context": ["docs/orders.md"],
  "checks": [
    ["yarn", "nx", "test", "orders"],
    {"argv": ["yarn", "nx", "typecheck", "orders"], "inputs": ["src/**", "tsconfig.json"], "timeout_s": 600, "baseline": true}
  ],
  "ui": {"summary": "Show a truncation note", "criteria": ["Shown only when total > shown"], "preserve": ["Columns, navigation"]},
  "visual": {"enabled": false, "reason": "User chose code-based verification"},
  "design": [{"figma": "https://www.figma.com/design/<key>/<file>?node-id=12-345", "url": "/checkout",
              "root": "main .checkout", "name": "checkout", "stream": "web"}],
  "architecture": {"id": "<receipt id>", "base": "fable", "acceptance": "Your amendments to that design",
                   "blockers": {"astra/b1": "Resolved by ..."}}
}
```

`design` is for a stream that implements a Figma design. Give the link to the frame (`figma`), the page that
shows it (`url`, a path) and the element that implements it (`root`): at launch the design is pulled into a
screen directory, the front end is started, the page is measured, and the layout check is declared. A screen
that exists already is named instead: `{"screen": "<dir>", "stream": …}`.

For a phone/tablet add `device: "mobile"` and `viewport: "390x844"` (or the tablet's size). Default device is
desktop regardless of width. Pair the exported frame with an equivalent root including its padding; use
the report's measurement line to inspect device, viewport, document width and root dimensions. An existing
content wrapper is a valid root when it corresponds to the exported content frame. `map.select` accepts
unique CSS selectors and does not exempt a selected element from geometry or styling checks.

How the front end starts is said once per repository, in `.agentgate.json`:

```json
{"design": {"screens": "apps/web/testing/design",
            "stand": {"command": ["yarn", "dev"], "ready": "http://localhost:4200",
                      "env_file": ".env.local", "graphql_env": ["VITE_GRAPHQL_URL"],
                      "cookies": [{"name": "session", "value": "…"}]}}}
```

The front end starts with `env_file` as it always does. `graphql_env` are the variables that point it at its
backend: when a screen has recorded answers in `fixtures/`, they are pointed at those instead.

A staged plan has no top-level `streams`: it lists `requirements` and `stages`, and every requirement is
covered by a stage. Each stage has `name`, `goal`, `covers`, `streams`, and optionally `acceptance`, `checks`
(added to the plan's) and its own `assessment`.

```json
{
  "request": "<the raw /dev arguments>",
  "assessment": {"size": "complex", "evidence": "..."},
  "brief": "What the whole task is, its invariants and non-goals",
  "requirements": [
    {"id": "R1", "text": "Totals are stored as integer cents"},
    {"id": "R2", "text": "The invoice page shows the new totals"}
  ],
  "stages": [
    {"name": "schema", "goal": "Store totals as integer cents", "covers": ["R1"],
     "acceptance": ["Existing invoices read back unchanged"],
     "assessment": {"size": "ordinary", "risks": ["migration"], "irreversible": true, "evidence": "..."},
     "streams": [{"name": "db", "domain": "backend", "files": ["prisma/schema.prisma"]}],
     "checks": [["yarn", "nx", "test", "billing"]]},
    {"name": "page", "goal": "Show the new totals", "covers": ["R2"],
     "streams": [{"name": "web", "domain": "frontend", "files": ["app/Invoice.tsx"]}]}
  ],
  "checks": [["yarn", "nx", "typecheck", "billing"]]
}
```

- `assessment` is required when the request has no mode flag. `size`: `trivial`, `ordinary`, `complex`.
  `risks`: `authorization`, `security`, `money`, `data-integrity`, `concurrency`, `migration`,
  `public-contract`. `uncertainty`: `low` or `material`.
- `streams[].files` are repository-relative regular files, including files to be created or deleted. Streams
  are disjoint. `after` orders waves. `done: true` keeps ownership without starting a writer. `effort`
  raises the writer's effort for one hard stream, up to the mode's ceiling; leave it out otherwise.
- `checks`: an argv array, or an object. `inputs` are globs (`**` crosses directories) whose content decides
  whether a result is still current; without them any change to the task's files invalidates it.
  `baseline: true` records the check before writing for diagnosis. A later nonzero result always blocks; a pre-existing failure is not a passing verification receipt.
  `no_reuse: true` for checks that depend on external state.
  `kind`: `test`, `typecheck`, `lint`, `build`, `measurement`, `runtime` or `command` (default).
- `requirements`: acceptance criteria for single-pass and staged plans, each `{id, text, method?}`.
  IDs are unique. `method` optionally requires `code`, `check` or `browser` proof. Every criterion must be
  verified before completion; a stage assesses only its `covers`, integration assesses the whole plan.
  A staged requirement already accepted cannot be changed by replanning. Older plans without requirements
  keep their existing behavior; a task with requirements needs the new structured review before finishing.
- `architecture.base` is the alias of the design the task follows (default: the first); writers and reviewers
  receive that design as amended by `acceptance`, not both designs.
- `ui`: exactly one of `{summary, criteria, preserve}`, `{design, acceptance}` or `{skip}`. Required when
  UI files (`.tsx .jsx .vue .svelte .html .css .scss .sass .less`) are in scope.
- `visual.scenarios[]`: `{id, screen, state, viewport: "1440x900", role, expected}`.
- Continuation of an unfinished task: the complete plan again plus `update` (what changed and why).
  At `awaiting-review` or `reviewed`, apply it with `replan`; `plan save` only saves the file. Replan keeps
  every owned file and all findings, invalidates review and regenerates managed design checks. It cannot
  resume a hold, change write authority or capture a new baseline. Staged plans use their between-stage flow.
- `review` and `clean`: `base_ref` (default `HEAD`) is where the reviewed diff starts.
- Timeouts: `writer_timeout_s`, `reviewer_timeout_s`, 120–7200; default is the mode's ceiling, else 1200.
- Architecture revision: add `revise: "<receipt id>"` and `revise_blockers: ["id"]` to the same plan.

## Quality assessment

The first reviewer receives `quality {requirements, rules, checks, browser}` and returns the object below
alongside `reports`. In light mode `review` returns the same inputs to the lead; supply the object as
`lead_review.quality`. Other lanes keep their ordinary report schema. No additional agent is started.

Preparation receives `review_rule_catalogue` (paths/scopes/modes/sources, not all skill bodies) and returns
`review_rules` metadata for the resolved scope, including unavailable instructions. Required missing files
produce a structured BLOCKED response before saving a runnable plan. Architects, scouts and scoped UI
designers receive the applicable instruction snapshots too. Policy changes invalidate architecture and
discovery reuse as well as review. Local skills remain ordinary files linked through repository instructions;
review rules consume their declared criteria without executing their workflows.

Plans with `design` always add built-in blocking `design.<id>.appearance|placement|controls|coverage` rules.
Their `required: true` means they cannot be waived as `not_applicable`. Missing evidence is `unavailable`.
The design packet supplies cropped and full-context pictures and the measurement report. A layout command's
exit 0 proves measured properties only; these independent judgements are still required before finish.

```json
{
  "requirements": [{
    "id": "R1", "status": "verified", "reason": "The recorded test exercises totals preservation",
    "evidence": [{"kind": "check", "ref": "<id from quality.checks>", "detail": "Test covers old and new totals"}]
  }],
  "rules": [{"id": "contracts", "status": "pass", "evidence": "Inspected the changed public entry points"}]
}
```

Each requested id appears exactly once. Requirement statuses: `verified`, `partial`, `missing`; every status
needs a reason. `verified` needs evidence and the requested method when declared. Proof kinds:

- `code`: `ref` is a scoped `file:line`; `detail` explains the inspected behavior.
- `check`: `ref` is a key in `quality.checks`; a current successful receipt and its log must exist. A
  preexisting failing check is not successful proof. `kind` on the check describes its verification scope;
  the reviewer still has to judge whether the command actually proves the criterion.
- `browser`: `ref` is an observed scenario in the current browser report, with its saved screenshot.

Rule statuses: `pass`, `block`, `not_applicable`, `unavailable`, with a concrete `evidence` explanation.
Mandatory rules accept only `pass` or justified `not_applicable`. Advisory results remain visible without
blocking. Missing mandatory instruction files refuse launch/review/finish. Local instruction snapshots are
included in packets; list supporting documents explicitly, as no network fetching or skill execution is implied.

`status.quality` includes the report, typed check receipts, whether the assessment is current, and remaining
problems. Freshness covers task content, criteria, applicable rule contents and current proof receipts. A
browser pass supplied to a review with quality criteria runs first so its observation can be assessed.
`recheck: true` bypasses review reuse for a new judgement, not any acceptance condition. Actionable defects
also belong in ordinary findings for triage/fix; missing proof alone calls for evidence or a hold.

## Other inputs

```json
// review
{"lead_review": {"summary": "What I read and verified", "findings": []},
 "visual": {"url": "http://localhost:4200", "origins": ["http://localhost:4200", "http://localhost:3000"]},
  "reviewer_timeout_s": 1800, "skip_checks": false, "recheck": false,
 "finish": {"summary": "Close the task in this call if nothing blocks"}}

// a finding (in lead_review)
{"id": "", "severity": "P1", "confidence": "high", "category": "validation", "path": "src/order.ts", "line": 42,
 "evidence": "...", "impact": "...", "remedy": "...", "preserve": "...", "boundary_break": false}

// triage
{"dispositions": [{"id": "<finding id>", "decision": "accepted", "evidence": "Verified in src/order.ts:42",
                   "files": ["src/extra.ts"], "basis": "", "duplicate_of": ""}]}

// fix
{"brief": "Root cause and the coherent remedy", "reassessment": {"evidence": "...", "changed_approach": "..."},
  "escalate": false, "streams": ["api"]}

// finish
{"summary": "What was delivered and verified", "architecture_evidence": "How it matches the accepted design",
 "visual_exclusions": [{"id": "scenario", "reason": "Fixture unavailable"}]}

// visual
{"url": "...", "origins": ["..."], "login": "", "notes": "", "entry_points": [], "storage_state": "/path.json",
 "timeout_s": 1800, "exclusions": [{"id": "scenario", "reason": "..."}]}
{"choice": {"enabled": true, "reason": "User asked", "scenarios": []}}
{"skip_reason": "Stand cannot start"}

// design
{"task": "...", "brief": "...", "usage_context": "Roles, frequency, decisions, data", "files": [], "context": [],
 "effort": "medium", "revise": "<design id>", "notes": "Lead notes for the revision"}

// consult
{"lanes": [{"name": "callers", "question": "Who calls X and with what invariants?", "files": ["src/x.ts"], "route": "fable:high"}]}
```

Decisions: `accepted`, `rejected`, `deferred`, `needs-approval`. Severities P0–P3. Findings at the mode's
blocking severities, boundary breaks, and anything `accepted` or `needs-approval` block `finish` until they are
resolved or rejected with evidence.

## Configuration

Built-in table/profile, then user extensions and user config, repository extensions and `.agentgate.json`,
then the personal `.agentgate/config.json`. Extensions add list items; explicit configs replace lists;
objects merge key by key. `config show` prints effective settings. `config extensions` also prints effective
review rules and `rule_sources` (definition files in precedence order).

```json
{
  "models": {"sol": {"provider": "codex", "model": "gpt-6.1-sol", "pool": "codex"}},
  "fallbacks": {"sol": ["sonnet", "opus"]},
  "modes": {"medium": {"backend": "sonnet:high",
                       "review": [{"axes": ["correctness", "architecture"], "route": "fable:high"}]}}
}
```

A route is `<model alias>:<effort>` with effort `low medium high xhigh max`. Providers: `claude`, `codex`.
`agentgate config set modes.medium.backend sonnet:high` edits the user file.

## Phases

`implementing → awaiting-review → reviewed → (correcting → awaiting-review → reviewed)* → complete`;
`held` and `cancelled` from any active phase. State lives in `~/.local/state/agentgate/`.
