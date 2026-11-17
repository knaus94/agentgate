# Stages

[Русский](ru/stages.md)

A large specification is not written in one breath. Done as one task, it produces one huge diff, one heavy
review and long correction rounds. `agentgate` splits it into **stages**: each stage is one logical outcome,
goes through the whole gate — write, check, review, correct, accept — and the next stage starts only when the
one before it is accepted. `agentgate run` moves from one stage to the next by itself.

Every plan has stages. A plan written with `streams` at the top is a plan of one stage, so there is one
shape, one validator and one set of rules ([decision 0003](decisions/0003-one-plan-shape.md)).

```
plan with stages
   │
   ├─ design          a mode with architects: once, for the whole plan; the architects may challenge the split
   ├─ stage 1         write → checks → review → correct → accept      (a commit, when isolated)
   ├─ stage 2         starts from what stage 1 left; reviewed on its own change only
   ├─ …
   └─ the whole       one more review of the entire change, for the seams between stages
```

## What a staged plan holds

- **Requirements.** What the specification asks for, as a short list with ids and, optionally, the kind of
  proof each needs (`code`, `check`, `browser`).
- **Stages.** Each has a name, a goal (its one outcome), the requirements it `covers`, its own streams, and
  optionally acceptance criteria, extra checks and its own assessment.

`covers` means "this stage works on the requirement". A requirement may be covered by several stages and is
**due at the last one**: there it must be verified in full. An earlier stage is told that it contributes and
is not held to the end-to-end behaviour; what a foundation stage must deliver by itself goes into the stage's
own acceptance criteria. This is what a reviewer's `partial` on a foundation stage used to trip over.

The plan is validated as a whole before anything starts, and every problem is reported at once:

- every requirement is covered by a stage;
- every stage outside light mode has a declared check, or the plan states `checks_waiver` — why no command
  can verify the change;
- streams of one stage own disjoint files; a later stage may take a file over;
- the size limits below.

A plan that could not be finished is therefore refused when it is saved, not after the work is done. The
saved brief (`.agentgate/plans/<name>/brief.md`) shows the requirements with the stage where each is due.

A good stage leaves the declared checks green and is small enough to read in one sitting. Foundations first
(schema, contracts), then behaviour, then the interface and the clean-up.

## How a stage runs

- **Its own mode.** The mode is computed per stage from the stage's assessment, so a data migration can run as
  ultra and the form after it as medium. A mode flag in the request applies to every stage.
- **Fresh workers.** Writers and reviewers of a stage start with a clean context. They are given the whole
  brief, the stage's goal, the requirements it works on and where each is due, what the earlier stages
  delivered (their writers' summaries and the contracts they left), and the list of later stages, which are
  not theirs to do.
- **Its own review.** Reviewers see the stage's change, measured from where the previous stage ended.
- **Kept on acceptance.** The stage's reviews, findings, decisions and check receipts are archived with the
  task, so the history of one stage is not overwritten by the next. The record of the stage is its writers'
  own summary, not prose written afterwards.

## Between stages

`run` accepts a stage as soon as everything it requires is current, and opens the next one in the same call.
It stops when a decision is missing, when a stage is blocked, or when what was learned makes the plan wrong;
`--step` in the request pauses after every accepted stage until you say to go on.

## After the last stage

Per-stage reviews do not see the seams: a contract that drifted from one stage to the next, a leftover of an
early stage, a requirement no stage met end to end. So in heavy and ultra, and whenever there are three or
more stages, the whole change gets one more review, measured from where the task began. A finding there goes
to the writer of the stage that owned the file last, in that writer's own session.

## Isolation

A staged task works in its own worktree ([isolated tasks](isolation.md)) when the repository says how to
prepare one (`worktree.setup` or `worktree.copy`). Then every accepted stage is a commit on the task's branch,
and `land` brings the whole in. `staging.isolate` is `auto` by default; `always` and `never` force it. Without
a worktree a stage boundary is a recorded tree, and nothing is committed.

## When staging is required

```json
{"staging": {"max_files": 25, "max_streams": 5, "isolate": "auto"}}
```

A plan that would change more files or have more streams than this in one pass is refused, with the advice to
stage it. Flags in the request:

| Flag | Meaning |
|---|---|
| `--staged` | the plan must be in stages |
| `--single` | one pass whatever the size — for a change that really is mechanical |
| `--step` | pause after every accepted stage until you say to go on |

Reviewing an existing change (`/dev review`) has no size limit.

## What it costs

- Each stage has its own review, with a whole-change review when the rule above requires it. Stage reviews
  bound the diff under consideration; the last review still covers the entire change.
- Stages run one after another. Work that would have run as parallel streams of one task takes longer when it
  is put into separate stages; streams inside a stage still run in parallel.

The panel (`agentgate watch`) lists the stages with what is accepted, what is running and what is ahead.

A `plan` request may describe the same stages as future implementation. Preparation saves the proposal and
its brief without starting writers or checks. A proposal starts only with `agentgate start --implement`,
which is the lead's statement that the user authorized the implementation; the plan itself is not edited to
say so.

## Amending the plan

Changing the plan in the middle of the work is ordinary. `agentgate amend --plan <name>` (or `--file`)
replaces the plan of the active task at any step. The plan carries `update`: what changed and why. To have
the plan revised for you, call `agentgate prepare` with `notes` while the task is active: the same read-only
worker sees the task's position and what it waits for, and returns the revised plan.

- **Accepted stages are frozen**, and so are the requirements they completed. The present stage keeps its
  name and the streams that already have a writer; files it already changed cannot leave its scope.
- **A requirement may be added, or moved** to the stage that completes it. Changing its text or removing it
  needs `authorized`: who allowed it, in what words. A requirement is never weakened to get past a review.
- **`rework: ["stream"]`** sends the amendment to those writers as new work, in their own sessions. A stream
  the amendment adds is new work by itself.
- **Only what is touched becomes stale.** Changing what is to be built has the code reviewed again. Moving a
  requirement or changing a criterion has the reviewer reassess the criteria in its own session, without
  reading the code again. A change that touches neither leaves every receipt valid.
- **An unfinished round stays unfinished**: finished streams keep their reports, blocked writers are still
  answered in their sessions.
- **An addition does not wait for its turn.** Named operations that only add requirements or checks may be
  sent while a command runs; the running command applies them before its next review and before it could
  finish the task.
- **A finished task is not closed until it lands.** Named operations sent to it add a stage after the accepted
  ones, with the streams of the last stage, whose writers continue in their own sessions.

A file admitted late — a generator's output, a file a finding needs — always has its before-image: the task
recorded the whole tree when it opened and at every stage boundary
([decision 0004](decisions/0004-evidence-on-git-trees.md)). Every amendment is logged with its reason and
what it changed; `agentgate show plan` lists the revisions.
