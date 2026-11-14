# agentgate reference — the plan

A plan is the brief, the criteria and the stages that deliver them. The preparation worker writes it; write
one yourself only for an exact change whose files you already know.

## One stage

```json
{
  "request": "<the raw /dev arguments>",
  "assessment": {"size": "ordinary", "risks": ["data-integrity"], "irreversible": false, "uncertainty": "low",
                 "evidence": "What was inspected and what remains uncertain"},
  "brief": "Requirements, non-goals, invariants",
  "requirements": [{"id": "R1", "text": "Orders preserve their totals", "method": "check"}],
  "streams": [
    {"name": "api", "domain": "backend", "files": ["src/order.ts", "src/orders/"], "brief": "optional stream detail"},
    {"name": "web", "domain": "frontend", "files": ["app/orders/**/*.tsx"], "after": ["api"], "effort": "high"}
  ],
  "context": ["docs/orders.md"],
  "checks": [
    ["yarn", "nx", "test", "orders"],
    {"argv": ["yarn", "nx", "typecheck", "orders"], "kind": "typecheck", "inputs": ["src/**", "tsconfig.json"], "timeout_s": 600, "baseline": true}
  ],
  "generated": ["src/generated/**"],
  "ui": {"summary": "Show a truncation note", "criteria": ["Shown only when total > shown"], "preserve": ["Columns, navigation"]}
}
```

## Several stages

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
     "streams": [{"name": "db", "domain": "backend", "files": ["prisma/schema.prisma", "prisma/migrations/"]}],
     "checks": [["yarn", "nx", "test", "billing"]]},
    {"name": "page", "goal": "Show the new totals", "covers": ["R1", "R2"],
     "streams": [{"name": "web", "domain": "frontend", "files": ["app/Invoice.tsx"]}]}
  ],
  "checks": [["yarn", "nx", "typecheck", "billing"]]
}
```

A plan has `streams` or `stages`, not both. Validation reports every problem at once.

## Fields

- **`assessment`** is required when the request has no mode flag. `size`: `trivial`, `ordinary`, `complex`.
  `risks`: `authorization`, `security`, `money`, `data-integrity`, `concurrency`, `migration`,
  `public-contract` — only what the change really touches. `uncertainty`: `low` or `material`. A stage may
  carry its own; the mode is computed per stage.
- **`streams[].files`** is what the stream owns: exact repository-relative files (existing or to be created),
  directories written with a trailing slash, and patterns (`*` within a name, `**` across directories). Streams
  of one stage must not be able to name the same file. What a stream actually changed is read from the
  working tree, so a generator's output with a computed name is owned by its directory. A file git ignores is
  tracked only when it is named exactly.
- **`after`** orders streams of a stage into waves. `done: true` keeps ownership without starting a writer.
  `effort` raises one hard stream, up to the mode's ceiling.
- **`requirements`**: `{id, text, method?}`; `method` requires `code`, `check` or `browser` proof.
- **`stages[].covers`** lists the requirements a stage works on. **A requirement is due at the last stage that
  lists it**: that stage must leave it verified. At an earlier stage its verdict is reported and does not
  block. Every requirement is listed by some stage. The pass over the whole change judges all of them again.
- **`checks`**: an argv array, or an object. `kind`: `test`, `typecheck`, `lint`, `build`, `measurement`,
  `runtime`, `command`. `inputs` are globs whose content decides whether a result is still current; without
  them a result is bound to the whole working tree. `baseline: true` records the result before any writing; a
  failure that existed before still blocks. `no_reuse: true` for checks that depend on external state. Above
  light mode every stage needs at least one check, its own or the plan's; when no command can verify the
  change, say why in `checks_waiver`.
- **`generated`**: patterns of generator output. A reviewer is shown such a file as one line and checks where
  it came from, not its lines. Lock files and `generated/`, `dist/`, `*.gen.*`, `*.min.*` are recognised
  without being listed, as is `linguist-generated` in `.gitattributes`.
- **`ui`**: exactly one of `{summary, criteria, preserve}`, `{design, acceptance}` or `{skip}`. Required when
  UI files (`.tsx .jsx .vue .svelte .html .css .scss .sass .less`) are in scope.
- **`visual`**: `{enabled, reason, scenarios: [{id, screen, state, viewport: "1440x900", role, expected}]}`.
  Browser verification is opt-in and off unless the user asks for it.
- **`architecture`**: `{id, base?, acceptance, blockers}` — the accepted design of heavy and ultra.
- **`design`**: a Figma design a stream implements; see `agentgate reference design`.
- **`base_ref`** (default `HEAD`): where the diff of a `review` or `clean` request starts. Such a plan may
  leave out `streams`: its scope is whatever differs from the base.
- **Timeouts**: `writer_timeout_s`, `reviewer_timeout_s`, 120–7200; default: the mode's ceiling, else 1200.

Limits: a one-stage plan with more than `staging.max_files` exact files or `staging.max_streams` streams is
refused with the advice to stage it; `--single` in the request allows it, `--staged` demands stages.

## Amending the plan of an active task

`agentgate amend` takes the complete revised plan with

```json
{"update": "What changed and why", "authorized": "", "rework": ["api"]}
```

- Accepted stages are frozen, and so is every requirement an accepted stage covers.
- A requirement may be added, or moved to the stage that completes it. Changing or removing one needs
  `authorized`: who allowed it and in what words. A requirement is never weakened to pass a review; give an
  intermediate result its own criterion instead.
- The present stage keeps its name and every stream that already has a writer. A file the stage already
  changed stays in its scope. Later stages are free.
- `rework` names streams of the present stage that get the amendment as new work, in their own sessions.
  Streams the amendment adds are new work without being named.
- What goes stale is only what the change touches: later stages — nothing; coverage, requirements, checks —
  the criteria are reassessed by the reviewer without reading the code again; brief, goal, scope, UI, design —
  the code review.
- Every plan the task ran under is kept as a revision with its reason: `agentgate show plan`.
