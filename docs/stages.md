# Stages

[Русский](ru/stages.md)

A large specification is not written in one breath. Done as one task, it produces one huge diff, one heavy
review and long correction rounds. `agentgate` splits it into **stages**: each stage is one logical outcome,
goes through the whole gate — write, check, review, correct, accept — and the next stage starts only when the
one before it is accepted.

```
plan with stages
   │
   ├─ design          heavy, ultra: once, for the whole plan; the architects may challenge the split
   ├─ stage 1         write → checks → review → correct → accept      (a commit, when isolated)
   ├─ stage 2         starts from what stage 1 left; reviewed on its own change only
   ├─ …
   └─ the whole       one more review of the entire change, for the seams between stages
```

## What a staged plan holds

- **Requirements.** What the specification asks for, as a short list with ids.
- **Stages.** Each has a name, a goal (its one outcome), the requirements it `covers`, its own streams, and
  optionally acceptance criteria, extra checks and its own assessment.

Every requirement must be covered by a stage; a plan that leaves one out is refused. That is what keeps a
requirement from being lost between the specification and the work. The saved brief
(`.agentgate/plans/<name>/brief.md`) shows the requirements with the stage that delivers each.

A good stage leaves the declared checks green and is small enough to read in one sitting. Foundations first
(schema, contracts), then behaviour, then the interface and the clean-up.

## How a stage runs

- **Its own mode.** The mode is computed per stage from the stage's assessment, so a data migration can run as
  ultra and the form after it as medium. Routes follow that stage's assessment and configured mode table.
- **Fresh workers.** Writers and reviewers of a stage start with a clean context. They are given the whole
  brief, the stage's goal and requirements, what the earlier stages delivered (their summaries and the
  contracts their writers left), and the list of later stages, which are not theirs to do.
- **Its own review.** Reviewers see the stage's change, measured from where the previous stage ended.
  Reviewer independence still excludes models that authored earlier stages or failed partial attempts;
  starting a new stage does not erase the task's author history.
- **Kept on acceptance.** The stage's reviews, findings, decisions and check receipts are archived with the
  task, so the history of one stage is not overwritten by the next.

## Between stages

The lead goes on to the next stage by itself. It stops when a decision is missing, when a stage is blocked, or
when what it learned makes the plan wrong; `--step` in the request makes it stop after every stage and wait
for you.

The stages still ahead may be revised between stages, with a stated reason. Accepted stages are frozen.

## After the last stage

Per-stage reviews do not see the seams: a contract that drifted from one stage to the next, a leftover of an
early stage, a requirement no stage met end to end. So in heavy and ultra, and whenever there are three or
more stages, the whole change gets one more review, measured from where the task began. Its findings go back
to a writer for the stage that owned the file last.

## Isolation

A staged task works in its own worktree ([isolated tasks](isolation.md)) when the repository says how to
prepare one (`worktree.setup` or `worktree.copy`). Then every accepted stage is a commit on the task's branch,
and `land` brings the whole in. `staging.isolate` is `auto` by default; `always` and `never` force it. Without
a worktree a stage boundary is a snapshot of the files, and nothing is committed.

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
| `--step` | hold after every accepted stage until you say to go on |

Reviewing an existing change (`/dev review`) has no size limit.

## What it costs

- Each stage has its own review, with a whole-change review when the integration rule above requires it.
  Stage reviews bound the diff under consideration; the integration review still covers the entire change.
- Stages run one after another. Work that would have run as parallel streams of one task takes longer when it
  is put into separate stages; streams inside a stage still run in parallel.

The panel (`agentgate watch`) lists the stages with what is accepted, what is running and what is ahead.

A `plan` request may describe the same stages as future implementation. Preparation saves the proposal and
its brief without starting writers or checks. The request remains read-only: after explicit implementation
authorization, update its action and save again before launch. Do not remove stages merely because planning
itself does not write application code.
