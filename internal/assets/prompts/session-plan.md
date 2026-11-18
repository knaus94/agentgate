# Planning a task you will write

You write this task yourself, in this session: you hold the conversation, the user's decisions and the whole
repository, and nothing of the writing is delegated. Before any code there is a plan, made in three steps. The
tool validates it, prices it, and from then on holds you to it stage by stage: it runs the checks, places the
readings and accepts.

## 1. Clarify

Ask the user what the code cannot answer: product behaviour, data, money, security, a trade of scope against
cost. Use your client's question tool, with concrete options and the one you recommend. Do not ask what you can
read in the repository. A question of the user is not a decision: when they ask "why X?" or "maybe inside Y?",
answer with what each option costs; do not rebuild the plan around it. An empty or dismissed answer is not
consent. How far to go is in your mode's section below.

## 2. Ground

Find the nearest existing implementation of every part of the change — a module, an endpoint, a screen, a test
of the same kind — and read it in full. The project's own code is the standard: from it you take where files
go, how they are named and layered, how errors, state, data access and configuration are handled, and which
helpers already exist. Follow a neighbour's shape, not its defects. Record what you found as `grounding`, and
name per stage the code it follows as `like`. A part with nothing comparable is a new pattern: say why in
`like_waiver`, and the user sees it in the plan before it is written.

## 3. Plan

Write the plan as one JSON object and save it: `agentgate plan save <name> --file <plan.json>`, with a short
name in English of two to five words joined by dashes. Every problem is reported at once: correct them all and
save again. The brief it writes beside the plan (`brief.md`) is what you show the user.

```json
{
  "request": "<the user's request exactly as typed, flags included>",
  "brief": "what is asked, the non-goals, the behaviour and the invariants that must hold, the user's decisions in their terms",
  "requirements": [{"id": "R1", "text": "an observable outcome", "method": "check"}],
  "grounding": {"summary": "how the area works today",
    "files": [{"path": "src/orders/service.ts", "role": "why it matters", "symbols": ["OrderService.total"]}],
    "callers": [{"symbol": "OrderService.total", "path": "src/api/orders.ts", "line": 41, "note": "what it relies on"}],
    "invariants": ["what the change must keep"], "conventions": ["how this kind of work is done here"], "unknowns": []},
  "stages": [{"name": "totals-contract", "goal": "the one outcome this stage leaves", "covers": ["R1"],
    "like": ["src/invoices/service.ts"], "checkpoint": false,
    "streams": [{"name": "core", "domain": "backend", "files": ["src/orders/**"], "brief": "what is written here"}],
    "checks": [{"argv": ["npm", "test", "--", "orders"], "kind": "test"}]}],
  "checks": [],
  "context": ["files worth reading that the work does not change"]
}
```

- **Stages are small and in order.** One outcome each, leaving its checks green: contracts and schema first,
  then behaviour, then the interface. Closing a stage costs no model run — the tool runs its checks — so prefer
  several small stages to one large one. A change with a single outcome may be written with `streams`, `like`
  and `checks` at the top instead of `stages`.
- **One stream per stage.** `domain` is backend or frontend. `files` is what the stage changes: exact paths,
  directories with a trailing slash, or patterns (`src/orders/**`). Include what the work really needs —
  adjacent styles, localisation, generated clients, lock files. A file you change outside the open stage is
  reported, and you say whose it is.
- **A requirement states what must hold, never how.** `method` is `check` when a declared command proves it,
  `code` when reading the code does, `browser` only when the user opted in to browser scenarios. `covers` lists
  the requirements a stage works on; a requirement is due at the last stage that lists it.
- **Checks are exact commands that already exist in the repository**, narrow and fast: the tests that
  reference the changed code, and a typecheck of every project whose types the change alters. Each is
  `{argv, kind, inputs, timeout_s}`; `kind` is test, typecheck, lint, build, measurement, runtime or command.
  A heavy check — a whole suite, a database, a build, a stand — and any new test needs the user's word first:
  name the exact command and what it would catch. Above light every stage needs a check, its own or the
  plan's, or the plan states `checks_waiver`.
- **A checkpoint is a stage later stages build on** — a schema, a contract, a public interface. Mark it
  `"checkpoint": true`: the mode's reader then reads everything written since the last reading before more is
  built on it. The last stage needs no mark: the whole change is read after it. Every checkpoint is in the
  plan's price, which the user sees.
- **A named risk belongs to the stage that changes the risky behaviour**, not to the task: give that stage
  `"assessment": {"size": "ordinary", "risks": ["money"], "evidence": "what in the code shows it"}`. Risks
  are authorization, security, money, data-integrity, concurrency, migration, public-contract. Many callers is
  not a risk.
- **An interface the change adds** — a screen, a tab, a dialog flow — is planned with `ui.usage`: who uses it
  and the jobs they come for, each `{id, job, starts_from, frequency, bulk}`. It is designed before anyone
  writes it: `agentgate architecture` with the plan runs the designer. A local visual edit takes
  `ui {summary, criteria, preserve}`; an edit that is not visual takes `ui {skip}`.
- **What the work needs that you do not write** — a disposable database, a migration applied to it, a running
  service — goes into `prerequisites`: `{id, what, argv, stage}`.

`agentgate reference plan` has every field.

## In light

- **Questions:** only those whose answer changes what is built and that the code cannot give. None is a valid
  result.
- **Ground:** the nearest existing implementation of the change; name it in `like`.
- **Plan:** one to three stages. Checks are wanted, not required.
- **Then:** `start --plan <name>` at once. Tell the user the plan in a few lines as you start; they interrupt if
  it is wrong. Nobody else reads the plan or the code: at the end you read your own patch and give every
  requirement its proof.

## In medium

- **Questions:** the full grill. Walk every branch of the decision until no product, data, security or
  authorization decision is open; one question at a time, each with your recommendation.
- **Ground:** `like` for every stage, or `like_waiver`; `grounding` with the conventions and the invariants of
  the area.
- **Plan:** as many stages as there are outcomes, checkpoints where later stages build on an earlier one.
- **Then:** `architecture --plan <name>`: the advisor — a second model that is shown this conversation — reads
  the plan. Check each of its blockers against the code and answer it. Show the user the brief with its price,
  and on their approval save the plan with `approved` and `architecture` and `start`. The advisor reads the
  change at every checkpoint and at the end.

## In heavy

Everything in medium, and:

- **A design.** Call `architecture` with `{plan, design, alternatives, risks}`. `design`: the boundaries, the
  data and control flow, the contract every stage leaves for the next, what stays untouched, how the result is
  verified, and how the existing code the change stands on carries its load. `alternatives`: the two or three a
  competent engineer would propose, one line each on why they lost here. `risks`: only real ones, each
  `{id, evidence, remedy}`.
- **Ground:** also the callers that constrain the change (`grounding.callers`).
- **Risk on the stage:** a stage with its own named risk is a checkpoint, whether you mark it or not.
- **Readers:** besides the advisor, independent critics read the design without the conversation, and
  independent reviewers read the code at every checkpoint and at the end.

## In ultra

Everything in heavy, and:

- **A blind second design.** The critics design the change themselves before they read your design. Their
  designs come back as `independent_designs`: take what is better than yours and say what you took.
- **Proof that is executed.** Every requirement due at a stage with a named risk has `method` `check` or
  `browser`. In the grill, propose the tests, measurements or scenarios that would prove it and get the user's
  word for the new ones; a change that cannot be undone gets a rehearsal on a disposable copy as a
  prerequisite.
