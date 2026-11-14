# 0007 — Preparation is one resumable conversation with a typed plan; no separate scout

## Context

`prepare` started a new read-only worker on every call. The worker explored the repository, drafted a plan
and returned it as a JSON *string* inside its structured answer. Fourteen calls were needed for one plan
([0001](0001-what-0.14-0.15-showed.md)):

- The plan's structure was invisible to the provider's schema enforcement, so format errors were found by
  the host validator — one per call, because validation stopped at the first.
- One correction was allowed inside a call. After that the lead started another call and another worker.
- An answer to a product question could only be delivered as `notes` to a new worker, which read the
  repository again.
- The worker returned `BLOCKED` for things that were not decisions: it could not save a file it was never
  asked to save, or it had not finished exploring.

After preparation a second worker, the scout, mapped the same area again for the writers and reviewers —
once per stage and once per changed contract, because its cache key held the stage brief and a digest of the
whole repository. Eleven scouts cost more input tokens than the ten writers they served. The project's own
measurement had already removed the scout from heavy and ultra ("an expensive model opens the files itself
whatever map it is handed").

## Decision

**One conversation.** A preparation has an identity and a worker session. `prepare` continues the open
preparation of the lead session: the user's answers and the lead's notes are sent into the same session,
which still holds everything the worker read. A new exploration starts only for a new request or on
`fresh: true`. When a task is active, `prepare` revises its plan in the same way and the result is applied
with `amend`.

**A typed plan.** The worker's answer schema contains the plan as a structure: stages, streams, checks with
their `kind` as an enumeration, requirements with their `method`. Both providers enforce the schema, so the
format errors of the log cannot be produced. Optional fields are nullable, as strict schemas require.

**Every problem at once, corrected in place.** Validation collects all problems of a plan. They go back
into the same session, up to three times per call.

**Questions are data.** A blocked preparation returns `questions: [{id, question, options, recommended}]`,
which the lead passes to its client's question tool unchanged and returns as `answers: {id: text}`. The
status `BLOCKED` is reserved for a missing product, data, security or authorization decision; the prompt
says what is *not* a blocker.

**No scout.** The preparation worker already returns a context pack — files and their roles, callers,
invariants, conventions. It is saved beside the proposal and handed to writers and reviewers of every
stage. A plan the lead wrote itself has no pack; the lead named the context when it wrote it. Stages do not
trigger exploration. Each stage's writers additionally receive what the earlier stages left: their
summaries and hand-off contracts.

The design-specific half of the preparation prompt (Figma catalogues, measurement roots, devices) is
attached only when the request or the active plan has a design.

## Alternatives

- **A stronger model for planning.** The planner is the cheapest and most leveraged role (Anthropic measured
  $0.46 of a $125 build, and found the build under-scoped without it). The log shows protocol failures, not
  reasoning failures; the route stays configurable (`modes.medium.scout`) and the protocol is what changed.
- **Fork the preparation session into the writers**, so they start with the repository already read. Both
  CLIs can fork a session, but only within one model, and the preparer is deliberately a cheaper model than
  the writer. The pack carries the conclusions across models.
- **Keep the scout with a better cache key.** A better key would cut the count, not the duplication of what
  preparation already did.

## Consequences

- A plan costs one exploration. Answers and corrections cost one message each. Measured live: a three-stage
  plan was accepted by the validator in the first round after 59 seconds and four commands; two answers to
  the worker's questions cost 20 seconds and one command, the reading of the answers.
- `modes.<mode>.scout` now names the preparation route only; the `scout` prompt and the per-launch scouting
  pass are removed. In medium a task launched from a hand-written plan no longer gets an automatic map.
- The preparation worker's session must be kept until the plan is started or abandoned; it is pruned with
  the checkout's other receipts.
