# 0004 — Evidence on git trees; scope as paths and patterns

## Context

When a task opened, the tool read every owned file into the task state as base64, once for the stage and
once for the whole task. Everything that asked "what was here before" read those snapshots. The design had
four costs:

- A file that was not on the list at launch had no before-image. Admitting one later (a generator's output,
  a file a finding needs) meant reconstructing it from git and from a manifest of dirty files, and refusing
  when that was impossible. Two of the 0.15 fixes and 190 lines of `replan.go` existed for this.
- The stage baseline and the task baseline were two maps that had to be kept in step.
- The state file reached 0.6–1.1 MB and was rewritten whole on every save, including the save after each
  writer's report.
- Check receipts without declared inputs were bound to `HEAD` plus a fingerprint of every changed file, so a
  commit of already verified bytes needed special code to keep its receipts.

## Decision

The before-image of a task is **a git tree of the whole working tree**, taken when the task opens and again
at every stage boundary and review.

- A snapshot copies the repository's index to a private file, runs `git add -A` against it and writes the
  tree. Objects go to a private object directory inside the task's state; the repository's object database is
  read through `GIT_ALTERNATE_OBJECT_DIRECTORIES`. The user's index, objects and refs are never written.
- The copy of the index keeps the modification time of the original. git trusts the cached stat data of an
  entry only when the index file is newer than the entry, and compares content otherwise. A copy stamped
  "now" loses that protection: an edit that keeps a file's size, made in the second the index was written,
  would be read from the cache as unchanged a second later. The first implementation had exactly this
  defect; a migration test found it, and a test of the evidence now holds it.
- The snapshot holds the user's uncommitted and untracked work exactly as it was. Ignored files are left out,
  except exact paths a stream declares, which are added by force.
- Every question about change is a comparison of two trees: what a stage changed, what changed since the
  last review, the patch a reviewer reads, the before-image of any one file, the lines a task added.
- The state stores tree ids: 40 characters where megabytes were.

Measured on a 2 200-file repository with uncommitted work: 0.04 s for the first snapshot, 0.01 s afterwards.

Bindings follow:

| Evidence | Bound to |
|---|---|
| A review | the raw difference (paths, modes, blob ids) of the scope between the stage's tree and now, plus the blobs of declared context and guidance files, plus the code contract |
| A check without declared inputs | the tree it ran against |
| A check with declared inputs | the content of those inputs, as before |
| Completion | the tree at completion |

A commit that records verified bytes does not change the tree, so the receipt-rebinding code is gone. A
rewritten `HEAD` no longer needs detecting: the baseline does not depend on it.

Changes outside the task's scope are the entries of the same difference that no pattern matches. They are
reported, as before, and refuse the private commit of an isolated task.

## Alternatives

- **Keep file snapshots and capture more eagerly.** "Capture more" has no bound short of the whole tree, and
  the whole tree is what git already stores efficiently.
- **`git stash create`.** It records tracked changes only; untracked files — a new migration, a new
  component — are the common case here.
- **Commits on a hidden ref of the user's repository.** Simpler plumbing, but it writes into the user's
  object database and ref namespace. The private object directory costs two environment variables.
- **A shadow repository beside the checkout**, as some editors keep. It duplicates every tracked blob; the
  alternates mechanism shares them.

## Consequences

- A file discovered at any time has a before-image. The amendment-baseline code and its refusals are deleted.
- Generator outputs and directories can be owned by pattern.
- Ignored files a stream owns only by pattern are not tracked. This is stated in the plan reference; an
  ignored build product is rarely something a reviewer should read.
- The tool must be run from the root of the working tree. It always was in practice: the old manifest mixed
  root-relative and directory-relative paths otherwise.
- Submodule contents are seen as one pointer, as before.
- Saved tasks carry file snapshots; migration builds trees from them once
  ([0010](0010-state-format-and-migration.md)).
