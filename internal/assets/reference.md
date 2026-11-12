# claude-dev reference

Every command prints JSON. Inputs are read from `--file <path>`, `--file -` (stdin) or `--plan <name>` (a plan
saved with `plan save`). Unknown fields are rejected.

## Commands

| Command | Input | Purpose |
|---|---|---|
| `parse` | raw request | parse the `/dev` arguments |
| `preview` | raw request | show mode handling and routes; no files, no models |
| `plan save [name]` | plan | validate and keep the plan in `.claude-dev/plans/<name>/` with a readable `brief.md` |
| `plan list`, `plan show <name>` | — | saved plans of this repository; print a brief |
| `plan link [folder]` | — | user's setup: keep `.claude-dev` in a synced folder and link it here |
| `route` | plan | mode decision, reasons and models; read-only |
| `architecture` | plan | design passes of heavy and ultra |
| `design` | design input | read-only UI designer |
| `consult` | consult input | parallel read-only questions (plan, research, verify) |
| `launch` | plan | open or continue the gate and run the writers |
| `answer` | `{stream, answer}` | reply to a blocked writer in its own session |
| `tell` | `{message}` | leave a note on the writers' shared board |
| `check` | `{argv}` or `--all` | run declared checks; exit code 2 when one fails |
| `review` | optional review input | declared checks, then review lanes and the optional browser pass; can finish |
| `visual` | visual input | browser pass, or record the choice / a skip |
| `triage` | `{dispositions}` | judge findings |
| `fix` | `{brief, reassessment?, escalate?, streams?}` | send accepted findings and failing checks back to the writers |
| `finish` | `{summary, ...}` | close the task |
| `land` | `{how?}` | isolated task only: bring it into the checkout — `apply` (default), `commit`, `branch` or `drop` |
| `hold` | `{reason, cancelled?}` | park the task |
| `resume`, `stop`, `status`, `watch` | — | lifecycle and observation |

Setup: `doctor`, `install [--zed] [--uninstall]`, `update [--check]`, `modes`,
`config show|path|init|set <key> <value>|unset <key>`, `prune`, `version`,
`accounts [--json] | discover [--save] | add <name> <claude|codex> <home> [--paid] | remove <name> | reset <name>`
(several subscriptions: limits, load spreading and failover are automatic).

## Request

`[-N] [-l|-m|-h|-x] [-w] [plan|research|fix|review|clean|verify|status|stop|help] [--fix] [--grill|--no-grill] [--preview] <task>`

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
  "architecture": {"id": "<receipt id>", "base": "fable", "acceptance": "Your amendments to that design",
                   "blockers": {"astra/b1": "Resolved by ..."}}
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
  `baseline: true` runs the check before writing, and a check that was already failing does not block later.
  `no_reuse: true` for checks that depend on external state.
- `architecture.base` is the alias of the design the task follows (default: the first); writers and reviewers
  receive that design as amended by `acceptance`, not both designs.
- `ui`: exactly one of `{summary, criteria, preserve}`, `{design, acceptance}` or `{skip}`. Required when
  UI files (`.tsx .jsx .vue .svelte .html .css .scss .sass .less`) are in scope.
- `visual.scenarios[]`: `{id, screen, state, viewport: "1440x900", role, expected}`.
- Continuation of an unfinished task: the complete plan again plus `update` (what changed and why).
- `review` and `clean`: `base_ref` (default `HEAD`) is where the reviewed diff starts.
- Timeouts: `writer_timeout_s`, `reviewer_timeout_s`, 120–7200; default is the mode's ceiling, else 1200.
- Architecture revision: add `revise: "<receipt id>"` and `revise_blockers: ["id"]` to the same plan.

## Other inputs

```json
// review
{"lead_review": {"summary": "What I read and verified", "findings": []},
 "visual": {"url": "http://localhost:4200", "origins": ["http://localhost:4200", "http://localhost:3000"]},
  "reviewer_timeout_s": 1800, "skip_checks": false,
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

Built-in table, then `~/.config/claude-dev/config.json`, then `.claude-dev.json` in the repository; objects
merge key by key. `claude-dev config show` prints the effective result.

```json
{
  "models": {"sol": {"provider": "codex", "model": "gpt-6.1-sol", "pool": "codex"}},
  "fallbacks": {"sol": ["sonnet", "opus"]},
  "modes": {"medium": {"backend": "sonnet:high",
                       "review": [{"axes": ["correctness", "architecture"], "route": "fable:high"}]}}
}
```

A route is `<model alias>:<effort>` with effort `low medium high xhigh max`. Providers: `claude`, `codex`.
`claude-dev config set modes.medium.backend sonnet:high` edits the user file.

## Phases

`implementing → awaiting-review → reviewed → (correcting → awaiting-review → reviewed)* → complete`;
`held` and `cancelled` from any active phase. State lives in `~/.local/state/claude-dev/`.
