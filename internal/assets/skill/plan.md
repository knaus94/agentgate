# The plan

A plan is the brief, the criteria and the stages that deliver them. You write it for a task you write
yourself — the `session` page has the four fields such a plan adds: `like`, `checkpoint`, `grounding` and
`approved`. With `--workers` the preparation worker writes it.

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

- **`mode`**: `light`, `medium`, `heavy` or `ultra`, stronger than a flag in the request; the `mode` page.
- **`assessment`** is required with `--workers` when neither the plan nor the request sets a mode; in a task you
  write it is optional and only places readings (a stage's own risk) and says what the facts suggest. `size`: `trivial`, `ordinary`, `complex`.
  `risks`: `authorization`, `security`, `money`, `data-integrity`, `concurrency`, `migration`,
  `public-contract` — only what the change really touches. `uncertainty`: `low` or `material`. A stage may
  carry its own; the mode is computed per stage. `load`, when the change touches a path whose cost grows with
  data or with how often it runs: what grows and how large, how often, the time allowed, and the source of
  each number. It does not change the mode; the architect designs for it, writers and reviewers read it in the
  brief, and the reviewer of correctness also reports the axis `performance`.
- **`streams[].files`** is what the stream owns: exact repository-relative files (existing or to be created),
  directories written with a trailing slash, and patterns (`*` within a name, `**` across directories). An
  entry that names a directory owns what is inside it: `migrations/*_orders` owns
  `migrations/2026_orders/migration.sql`. `*`, `dir/**` and a name with an extension (`*.ts`) name files.
  Streams of one stage must not be able to own the same file; `pages/x/api/**` and `pages/x/*` can share
  none. What a stream actually changed is read from the working tree, so a generator's output with a computed
  name is owned by its directory. A file git ignores is tracked only when it is named exactly.
- **`after`** orders streams of a stage into waves. `done: true` keeps ownership without starting a writer.
  `effort` raises one hard stream, up to the mode's ceiling.
- **`requirements`**: `{id, text, method?}`; `method` requires `code`, `check` or `browser` proof.
- **`stages[].covers`** lists the requirements a stage works on. **A requirement is due at the last stage that
  lists it**: that stage must leave it verified. At an earlier stage its verdict is reported and does not
  block. Every requirement is listed by some stage. The pass over the whole change judges all of them again.
- **`checks`**: an argv array, or an object. `kind`: `test`, `typecheck`, `lint`, `build`, `measurement`,
  `runtime`, `command`. `inputs` are globs whose content decides whether a result is still current; without
  them a result is bound to the whole working tree. `baseline: true` records the result before any writing; a
  failure that existed before still blocks. `no_reuse: true` for checks that depend on external state. A
  stage runs the plan's checks and its own; the last stage runs every declared check, because it leaves every
  requirement verified (an unchanged result is reused). A check that passed on the same files of the task and
  fails now failed outside the code: it comes to you as `setup`, not to the writers. Above
  light mode every stage needs at least one check, its own or the plan's; when no command can verify the
  change, say why in `checks_waiver`.
- **`generated`**: patterns of generator output. A reviewer is shown such a file as one line and checks where
  it came from, not its lines. Lock files and `generated/`, `dist/`, `*.gen.*`, `*.min.*` are recognised
  without being listed, as is `linguist-generated` in `.gitattributes`.
- **`ui`**: exactly one of `{summary, criteria, preserve}`, `{design, acceptance}` or `{skip}`. Required when
  UI files (`.tsx .jsx .vue .svelte .html .css .scss .sass .less`) are in scope. An interface the change adds
  carries `usage`: `{who, jobs: [{id, job, starts_from, frequency?, bulk?}]}` — what a person comes to do and
  what they have in hand when they begin. Such a plan starts only with an accepted design
  (`ui {design, acceptance, usage}`, from `architecture`), and its reviewers judge the code by those jobs on
  the axis `usability`.
- **`prerequisites`**: `[{id, what, argv?, stage?, after?}]` — what the work needs to exist that no writer
  makes: a disposable database, a migration applied to it, a running service. `argv` is how it is made;
  `stage` the stage that needs it (the first by default); `after` a stream of that stage it is made from. The
  writers of the stage wait for one without `after`; the checks after that stream wait for one with it. A need
  `setup` asks you for it; `run {prepared: [ids]}` says it exists.
- **`env_file`**: the absolute path of a private file (mode 600) outside the repository, with `NAME=value`
  lines: every declared check gets those variables. The plan keeps the path; answers show the names only. Set
  it in the saved plan, or with `amend {update, env_file}`.
- **`visual`**: `{enabled, reason, scenarios: [{id, screen, state, viewport: "1440x900", role, expected}]}`.
  Browser verification is opt-in and off unless the user asks for it.
- **`architecture`**: `{id, answers?, blockers?, acceptance?}` — the design the task follows and the user's
  decisions on it; see `agentgate reference inputs`.
- **`design`**: a Figma design a stream implements; see `agentgate reference design`.
- **`base_ref`** (default `HEAD`): where the diff of a `review` or `clean` request starts. Such a plan may
  leave out `streams`: its scope is whatever differs from the base.
- **Timeouts**: `writer_timeout_s`, `reviewer_timeout_s`, 120–7200; default: the mode's ceiling, else 1200.

Limits: a one-stage plan with more than `staging.max_files` exact files or `staging.max_streams` streams is
refused with the advice to stage it; `--single` in the request allows it, `--staged` demands stages.

## Amending the plan of a task

`agentgate amend` takes named operations on the present plan, or the complete revised plan. Both carry
`update` — what changed and why — and the same rules hold for both.

```json
{"update": "The generator also writes the client; R3 is completed by the page stage",
 "scope": [{"stream": "api", "add": ["src/generated/"]}],
 "checks": [{"stage": "page", "add": [["yarn", "nx", "typecheck", "client"]], "remove": ["e2e"]}],
 "requirements": [{"id": "R3", "stage": "page"}, {"id": "R7", "text": "Expired claims are refused", "method": "check"}],
 "timeouts": {"writer_s": 5400, "reviewer_s": 2400},
 "design": [{"design": "checkout", "set": {"root": "main [data-checkout]"}}, {"add": {"figma": "https://…", "stream": "web"}}],
 "mode": "heavy", "architecture": {"id": "<receipt id>", "answers": {"retention": "30 days"}},
 "authorized": "", "rework": ["api"]}
```

- `scope`: files a stream gets; `stage` defaults to the present one. In the pass over the whole change a
  stream is named `stage.stream`, as `needs` and `answer` name it.
- `checks`: added to the plan's own list (every stage runs it) or to a named stage; `remove` names a check by
  its id or by a part of its command only it contains.
- `requirements`: a new one (`text`, optionally `method`; due at the last stage unless `stage` says), a
  changed one (`text`: needs `authorized`), or one moved: `stage` is where it becomes due — that stage covers
  it and no stage after it does.
- `design`: `add` an entry, or name one (`design`: its screen, name or figma link) with `set` for the fields
  that change, or `remove: true`.
- `architecture`: the receipt the task follows from now on — a revision the architect returned — with the
  user's answers. Name in `rework` the streams whose written code the revised design changes.
- `env_file`: the private file of the checks' variables.
- The complete plan: `agentgate show plan` prints it under `result.plan`; send it back changed, with `update`.

While a call runs, an `amend` that only adds requirements or checks waits for it (`pending_amendments`) and is
applied before the next review; anything else is refused until it ends. A finished task that is not landed
takes named operations as a new stage after its accepted ones (the `finished` page).

- Accepted stages are frozen, and so is every requirement an accepted stage covers. The pass over the whole
  change takes additions only: more files for the stream of an accepted stage (`scope`), a new requirement, a
  check. What an accepted stage built and the user no longer wants is removed by a stage added after it,
  before the last stage is accepted.
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
