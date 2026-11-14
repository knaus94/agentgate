# 0010 — State format 2 and the migration of saved tasks

## Context

The decisions in [0002](0002-one-state-machine.md), [0003](0003-one-plan-shape.md) and
[0004](0004-evidence-on-git-trees.md) change what a saved task holds: a step and a pause instead of a phase
and a remembered phase, one plan with stages instead of a plan beside an "epic", tree ids instead of base64
snapshots, receipts bound to trees and keys instead of digests of snapshot maps.

A user upgrades with tasks on disk. Some are finished and are only read by the panel. Some are active, and
one of them may be days of work that is half reviewed. A task's state is also read by *other* sessions: the
ownership check that protects files between parallel tasks reads every state in the checkout, and a state it
cannot read blocks every new task there.

## Decision

**A version field.** State written by 0.16 carries `"version": 2`. A state without it is the earlier
format.

**Reading translates in memory and writes nothing.** `migrateState` is a pure function of the bytes it is
given. The panel, `status`, the Stop hook and the ownership check of another session read an earlier task
through it without touching the file. `status` says the task was saved by an earlier version; the panel
shows its stages, workers and findings as before.

| Earlier state | Present state |
|---|---|
| `implementing`, `correcting`, `staged` | step `write` |
| `awaiting-review`, `reviewing`, `reviewed` | step `verify` |
| `complete`, `cancelled` | `done`, `cancelled` |
| `held` + the phase it interrupted | that phase's step, plus a pause chosen by the recorded reason: stopped by the user → `user`; confirm each stage → `step`; a blocked, failed or interrupted writer → no pause, the stream's own status says it; anything else → `decision` |
| stage plan + epic + stage index + stage records | one plan with stages, the position and the accepted stages |
| the integration pass | the pass over the whole change |
| free text among the context files | a note at the end of the brief (the present validator takes paths only) |

The mode of a stage that had not opened yet was not decided in the earlier format. It is decided by the
first `run`, from the plan's assessment or the request's flag, exactly as at a start.

**The origin trees are built once, by the task's own session.** The first operation of the owning lead
session — under the task's lock, inside its checkout or worktree — builds the two trees the task is measured
from and saves the state in the present format. The original file is kept beside it as `state.v1.json`.

An origin is *the working tree as it is now, with the task's files put back to their recorded
before-images*. The earlier format recorded the before-image of owned files only and held that nothing else
is the task's change; this construction says the same thing as a tree. Building it from the commit the task
started at was the first implementation and was replaced: it reported every unrelated uncommitted file of the
user as a foreign change, and it failed when that commit was no longer in the repository.

**Evidence is not carried over.** Reviews and check receipts of the earlier format were bound to digests
this version does not compute. After the upgrade the declared checks and the review of the present position
run once more. Findings, the lead's decisions on them, writer sessions, accepted stages and their hand-offs
are kept, so the writers and reviewers continue in their own sessions.

**Old command names answer with their replacement.** `launch`, `review`, `triage`, `fix`, `finish`,
`replan` and `hold` are not aliases: a lead session that began under the earlier protocol is told which
command does that now and to read `agentgate guide` again. `continue` is the one exception: `run` does
exactly what it did and more, so it is accepted as another name for `run`.

## Verification

The translation was run over copies of eleven real saved tasks in a temporary directory; the originals were
not opened for writing.

- All eleven translate and pass the state validation: eight finished, one cancelled, two active.
- The active staged task (stopped by its user in the third of four stages) reads as `verify`, paused by the
  user, with `resume` and `stop` as its only actions. Upgraded against its repository, its plan resolves
  under the present rules after one context note is moved to the brief, the present stage shows 7 changed
  owned files and no foreign ones, the whole task 24, and the first `run` would verify. Its state file is
  52 kB where it was 2.9 MB.
- The panel, the JSON snapshot, `status` and the Stop hook were run on the copies before any upgrade and left
  them unchanged.

The dry run found five defects that were fixed before this record was written: the origin built from a
commit (above); a finished isolated task that was not yet landed had no origin and no key, so the Stop hook
would have reopened it; the modes of later stages were copied from the present one; the first review after
an upgrade compared against an empty tree; a context entry of free text made the whole plan invalid.

## Alternatives

- **Refuse earlier tasks: "finish it with the version that started it".** Honest and simple, and it strands
  exactly the tasks 0.15 could not finish — the ones this version exists for.
- **Convert every state on installation.** Trees have to be built inside each task's checkout, some of which
  no longer exist, and a conversion that fails halfway leaves a mixed directory. Translating on read needs no
  such moment.
- **Keep the earlier digests alive to prove that a review is still current.** It would save one verification
  per migrated task and keep the snapshot code that [0004](0004-evidence-on-git-trees.md) removes.
- **Aliases for every old command.** `review` and `finish` have no single successor: an alias would either
  do more than its name says or refuse in new words.

## Consequences

- An upgrade in the middle of a task costs that task one extra run of its checks and one review.
- There is no way back in place: 0.15 cannot read format 2. Going back means restoring `state.v1.json` by
  hand, and loses what was done since.
- A lead session that is still open from before the upgrade has the old guide in its context. Its first old
  command is refused with the new name; it must read `agentgate guide` again.
- The translation is held by two tests over one anonymized task of the earlier format: reading it without
  writing (position, pauses, the plan, the panel, another session's ownership check), and converting it in its
  own session and running it to the end.
