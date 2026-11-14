# 0003 — One plan shape: every task has stages; a requirement is due at its last stage

## Context

A task was either single-pass (`streams`) or staged (`stages`). The staged form was run by keeping the whole
plan in one field (`epic`) and a synthesized single-pass plan of the current stage in another, rebuilt by
validation every time it was needed. Its brief was text assembled from the plan and from the summaries of
accepted stages, and that text took part in the digests that bind a review and a context pack — so finishing
a stage changed keys that had nothing to do with it. "A task keeps its shape" was a rule with its own
refusals.

Two fields carried more than they could express:

- `request` — the raw `/dev` arguments inside the plan decided whether the plan was a proposal or an
  implementation. A planning request could therefore not hold the stages it proposed (fixed in 0.14.1 by a
  special case), and authorizing the implementation meant editing the string and saving the plan again.
- `covers` — "the stage delivers this requirement". A planner that listed every requirement a foundation
  stage *works towards* made that stage unable to finish. The reviewer's `partial` was correct, the plan was
  wrong, and it cost a release to make the plan correctable.

`streams[].files` had to name every file in advance. Generator outputs with computed names, a forgotten
neighbouring stylesheet and a directory written where a file was expected each stopped a run.

## Decision

**One shape.** A plan is a list of stages. A plan written with top-level `streams` is normalized on entry
into one stage that covers every requirement; nothing downstream knows the difference. The task holds one
plan and an index of the current stage. What a worker of a stage is told is rendered from the plan when the
assignment is written; it is not stored and takes part in no digest.

**A requirement is due at the last stage that covers it.** `covers` now means "this stage works on the
requirement". The last stage that lists it must leave it verified; at an earlier stage its verdict is
reported and does not block. The pass over the whole change still judges every requirement. A planner's
natural over-listing is therefore harmless, and the remaining mistake — a requirement listed only at a stage
that cannot complete it — is corrected by an amendment that touches no code
([0006](0006-amendment-is-ordinary.md)).

**The command authorizes, not the plan.** `prepare` (or `plan save`) saves a proposal; `start` runs it. A proposal that came
from a read-only planning request is refused by `start` until it is given `--implement`, the lead's
statement that the user authorized the work. The plan is not edited to change what it is allowed to do.

**Scope is paths and patterns.** A stream owns exact files, directories (`dir/`) and globs (`src/**/*.ts`).
What a stream actually changed is read from the difference between two trees
([0004](0004-evidence-on-git-trees.md)), not from the list. Patterns of streams that write at the same time
must not be able to overlap; the check is conservative and says which two patterns to narrow.

**A plan that cannot finish is refused before it starts.** Everything `finish` used to discover at the end
is checked by validation: at least one declared check for a writing task above light (or an explicit
`checks_waiver` with its reason), a route for an independent reviewer that is not a writer's model, a
browser-evidence requirement without an opted-in scenario matrix, an accepted architecture where the mode
needs one. Validation reports every problem at once.

## Alternatives

- **Keep two shapes and share more code.** The shapes differ only in the number of stages; keeping both
  keeps every "which plan do I mean" decision.
- **Per-stage `delivers` next to `covers`.** A second list is another thing a planner fills in wrongly. One
  list with a positional rule needs no new field and matches how plans are actually written.
- **Derive scope entirely from the diff and drop ownership.** Ownership is what lets two sessions share a
  checkout and what confines a writer; the declared scope stays, only its granularity relaxes.

## Consequences

- The fields `epic`, `stage_view` and the phase `staged` disappear; the integration pass is the stage after
  the last one, not a separate synthesized plan.
- A design reference binds to its stream by name wherever that stream is, so a staged plan can carry design
  work in one of its stages; before, design plans were single-pass in practice.
- Staging limits count declared exact files; a pattern has no size before the work, so an oversized stage is
  no longer always caught in advance.
- Saved plans with `streams` or `stages` keep working unchanged.
