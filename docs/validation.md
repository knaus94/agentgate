# Validation record

This record distinguishes live integrations from controlled provider fixtures and does not claim exhaustive
model quality, arbitrary specification size or every possible execution environment. It has two parts: what
was run for the 0.16.0 source, and the release validation of 0.14.0, which is kept as it was written.

## 0.21.5

Checked on 2026-11-18 against `aff28fb` (0.21.4). The interface scenario now requires the designer's packet to
carry the plan's `ui.criteria` and `ui.preserve`; on 0.21.4 it fails there.

| Check | Actual result |
|---|---|
| `go test -count=1 -run TestInterface ./internal/dev` on 0.21.4 with the stricter scenario | FAIL (the packet has neither key) |
| `gofmt -l internal/dev`, `go vet ./internal/dev` | clean |
| `go test -count=1 ./internal/dev ./internal/assets ./cmd/agentgate` | PASS |

The prompt rule on neighbouring screens is text for the designer; no check verifies that a design follows it.
The release workflow runs the full suite with `-race` before publication.

## 0.21.4

Checked on 2026-11-17 against `36bf995` (0.21.3). The scenario fixture of an interface plan gives its backend
stream the directory `.github/workflows/`; on 0.21.3 both architecture scenarios on it fail with "an exact
file path is needed here, not a pattern", as in the reported session.

| Check | Actual result |
|---|---|
| `go test -count=1 -run TestInterface ./internal/dev` on 0.21.3 with the fixture | FAIL (2 tests, the reported error) |
| `gofmt -l internal/dev`, `go vet ./internal/dev` | clean |
| `go test -count=1 ./internal/dev` | PASS |

The release workflow runs the full suite with `-race` before publication.

## 0.21.3

Checked on 2026-11-17 against `5231866` (the unpublished 0.21.2). Its release workflow failed in
`internal/provider`: a test reads the built-in route `sol:high` and still expected `gpt-6.1-sol`. The 0.21.2
check ran only `internal/config` and `cmd/agentgate`.

| Check | Actual result |
|---|---|
| `go test -count=1 ./internal/provider ./internal/accounts ./internal/config ./cmd/agentgate` | PASS |

The other tests that name a model declare their own routes. The release workflow runs the full suite with
`-race` before publication.

## 0.21.2

Checked on 2026-11-17 against `667d7f6` (0.21.1). Only the models of the built-in aliases `sol` and `astra`,
their examples in the documentation and the version changed.

| Check | Actual result |
|---|---|
| `gpt-6.2-sol` and `gpt-6.2-astra` in the local Codex model list (`~/.codex/models_cache.json`) | present |
| `go test ./internal/config ./cmd/agentgate` | PASS |

The release workflow runs the full suite with `-race` before publication. No model was run.

## 0.21.1

Checked on 2026-11-17 against `3d4696d`. The release workflow of 0.21.0 failed in `go test -race ./...`: two of
the new scenario tests appended what parallel architects and reviewers saw to a shared slice without a lock.
The local `make check` of 0.21.0 had passed; the race did not show there.

| Check | Actual result |
|---|---|
| `go vet ./internal/dev` | PASS |
| `go test -race -count=20 ./internal/dev -run 'TestInterface|TestPrerequisite|TestEnvFile|TestPending|TestFurther'` | PASS |

Only the tests changed. The release workflow runs the full suite with `-race` before publication.

## 0.21.0

Checked on 2026-11-17 against `a5e07d5` (the 0.21 brief on 0.20.2).

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS; Node core suite 19 of 19 |
| `go test ./internal/dev` without `-race` | PASS |
| Dry run | not run: not authorized for this release; its preparation scenarios still send `setup` as a list |

Six scenario tests are new (`internal/dev/interfaces_test.go`): a plan with `ui.usage` is refused at `start`,
and `architecture` designs the interface first — a designer that did not walk a job is refused — then the
architects on it, and the accepted design keeps the architecture valid; the first review lane of a stage that
writes interface code gets `usability`, and none without `usage`; a prerequisite holds the writers, one made
from a stream holds the checks after it, and `run {prepared}` releases them; a check gets the variables of
`env_file` and the task keeps none of their values, while a file others can read is refused; an `amend` that
only adds, sent while the session is busy, waits and reaches the reviewer's criteria before the task finishes,
while one that changes the mode is refused; `amend` on a finished task opens the stage `further`, whose writer
continues the last stage's session. Existing tests changed with the protocol: scripted preparation answers send
`setup` as an object.

Not checked: what real models do with the new prompts — the designer walking jobs, the architect and the
preparation worker answering a question with a comparison, the reviewer judging `usability`. That shows only
in real sessions. No model was run.

## 0.20.2

Checked on 2026-11-17 against `6ffe342` (0.20.1). Only the built-in table, the Codex profile, tests and
documentation changed.

| Check | Actual result |
|---|---|
| `gofmt -l`, `go vet` of `internal/config`, `cmd/agentgate`, `internal/dev` | PASS |
| `go test` (without `-race`) of `internal/config`, `cmd/agentgate`, `internal/dev`, `internal/provider`, `internal/accounts` | PASS |
| The Codex profile over the new built-in table | invalid until the profile set its UI designer per mode (it inherited `opus:*` with Opus removed); PASS after |

Tests adapted to the new table: the built-in table, `config unset` restoring the default backend, the default
consultant. The full `make check` with `-race` and the browser scenarios run in the release workflow before
publication. No model was run.

## 0.20.1

Checked on 2026-11-16 against `58a92aa` (0.20.0).

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| Dry run | 148 of 148 promises in 12 scenarios; the `design` scenario calls the hook through the real binary during the task and after it |
| The `design` scenario on 0.20.0 | 32 of 34: the two promises about the reminder fail |
| Installed Codex (0.169.0) | its binary names the `UserPromptSubmit` hook and `additionalContext`, the same wire shape as Claude Code |

Tests: the hook's note without a task, during a task with an accepted design and after the task; the command
line dispatches on `hook_event_name`; install keeps a foreign `UserPromptSubmit` hook and uninstall removes only
ours. Not checked: that a lead heeds the reminder — that shows only in real sessions. No model was run.

## 0.20.0

Checked on 2026-11-16 against `958ef03` (0.19.2).

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| Dry run | 144 of 144 promises in 12 scenarios |
| The new `design` scenario | 30 of 30 through the real binary and sandbox: a heavy pass of two designs and the closing round, a start held by an unanswered question and by another answer, a revision on the answer, a revision in the middle of a task with `plan_changes`, `amend {architecture}`, the `performance` axis on the correctness lane only |
| The same scenario on 0.19.2 | stops at its first step: the plan's `load` is an unknown field |

Tests adapted to the closing round and the new acceptance: heavy and ultra passes, the accepted design a writer
receives, the saved plan, project rules. New: the architect's question holds the start and another answer goes
back to it; a design is revised in the middle of a task; load facts reach the workers and add the axis.

**A live run on the cheapest routes**, asked for by the maintainer: every role on `sonnet:low` or `sol:low`,
an isolated configuration and state, a repository of five small files, eleven model calls.

| Step | Observed |
|---|---|
| `prepare` | found the full scans of the existing code and the 15-second poll, and returned `BLOCKED` with two questions: the size of the largest case and the time allowed. With the answers: `READY`, `assessment.load` filled with the numbers and their sources, the measurement named in `setup` |
| `architecture`, two architects with critique | both providers accepted the design schema; each design listed three alternatives; one critique raised a blocker; the closing round answered it under its id as resolved and took over the other design's question |
| `start` with another answer | refused, naming the revision |
| `architecture {revise, answers, notes}` with a doubt of the user | the architect agreed, replaced its mechanism, listed the rejected alternatives, asked one new question and returned four `plan_changes` |
| `start` | found a defect: applying the architect's own `plan_changes` — a file leaves the stream — unbound its receipt, because a one-stage plan was bound to its files. Fixed, with the binding of 0.19 still accepted |
| `start` after the fix | the writer built the revised design; the check passed; the reviewer reported `correctness`, `architecture`, `simplicity` and `performance` |

Spent: about 105k fresh, 337k cached and 11k output tokens on Codex, and about 112k cache-write, 155k
cache-read and 14k output tokens on Claude; the worker account's weekly meter stayed at 0%. In that run the
closing round took 21 s of a 127 s pass. The run also showed the preparation worker writing a mechanism into a
requirement, which the revision then had to reword; its instructions now forbid that.

Not checked on models: a revision in the middle of a task and `amend {architecture}` (covered by the dry run
and a test), ultra and heavy on their own models, and the lead's side of the guide.

## 0.19.2

Checked on 2026-11-16 against `feade44` (0.19.1).

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| Dry run | 114 of 114 promises in 11 scenarios; the `prepare` scenario now loses an answer twice, before any task and after a done one, and reads it from `status` |
| The same `prepare` scenario on 0.19.1 | 20 of 22: `status` shows neither the open questions nor where to go after a done task |

`TestAnswersGoToTheSameWorker` also asserts that `status` shows the questions of a blocked preparation. No
model was run.

## 0.19.1

Checked on 2026-11-16 against `fc7db9f` (0.19.0).

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| Dry run | 109 of 109 promises in 11 scenarios; the harness gives medium no design pass, as its scenarios start from a plan |

The scenario tests now run on the table they were written for (the built-in table of 0.19.0), set by the
fixture, so they keep testing escalation, substitution and continuation rather than the shipped table; the
tests of the built-in table check the new one. The test of the removed rule was deleted; two tests that
asserted its refusals now assert that a reviewer on the writer's model runs as configured. No model was run.

## 0.19.0

Checked on 2026-11-16 against the source based on `4497e12` (0.18.0). The work came from two plans: an account
for the lead with the limits a model has of its own, and what the lead sessions on 0.18.0 showed (two sessions,
88 operations, the morning of 2026-11-16).

### Automated checks

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| Go tests | 227 top-level tests (`go test -list`), six of them new; zero failures |
| Node core suite | PASS: 19 tests |
| Dry run: the real binary and sandbox, workers replaying recorded answers | 109 of 109 promises in 11 scenarios |

`make design-browser` was not run: the checker and the browser tools did not change; the design kit's Go
extraction did, and its tests ran.

New tests: an account kept for the lead takes no worker, under either directory of its subscription; a general
weekly limit at 100% beside a Fable limit at 50% in the three states of what is known, with a five-hour window
stopping both; the pass over the whole change takes a file for the stream of an accepted stage and a new
requirement and refuses any other change to it; a browser pass that observed its scenarios is recorded with what
it could not establish, and one that observed nothing fails; scenarios are due at their stage and a skip covers
one stage; a substitution by authorship says so, and a browser reviewer with no permitted route refuses
`start`. The input test now covers what git ignores, a pattern into an ignored directory, a link inside the
repository and one leading outside. Existing tests were adapted where wording or a contract changed: the input
tests, the Stop hook's sentence, the panel's header and the provider's skip function.

The dry run gained the scenario `whole` (three stages, the pass over the whole change finds a file no stream
owns, `amend` gives it and a requirement, the writer finishes). On 0.18.0 that scenario holds 3 of 4: the
amendment is refused. The dry run also found a defect the unit test had missed — a stream's files are sorted
when the plan is resolved, so an added file that sorted first was refused — fixed, and the unit test now adds
such a file.

### Measured on the user's machine

| What | 0.18.0 | 0.19.0 |
|---|---|---|
| An open panel on the running task of a large repository | 65% of a core, 10.7 MB read per second | about 0.5% of a core, no file content read between changes |
| An open panel on a finished task | 6.7% of a core | 0.9% of a core |
| One frame with verification (`watch --once`) | 6.7 s, 184 369 directories opened | one tree record: about 0.2 s of input work |
| The declared inputs of five checks of the skinhole task | 1.66 s, 5 786 files (with `node_modules`) | 0.21 s with the tree record, 1 418 files |

The first two rows were sampled from `/proc` over 20–60 seconds; the input row with a throwaway test on the
task's worktree, deleted afterwards. A direct before/after of `status` on the same task was not possible: by the
time the build was ready the task had moved on and had no receipts.

### By hand

- `agentgate figma node` under `bwrap --ro-bind / /`: 0.18.0 fails on `.extract.lock: read-only file system`;
  0.19.0 answers.
- `go build` in a writer-like sandbox: fails on `~/.cache/go-build` without `GOCACHE`, passes with it in a
  writable task directory.
- `config validate` and `modes` on the user's configuration name heavy and ultra: `visual_reviewer` is
  `astra-v:high` with no fallback while those modes write on the same model.
- With a build of this source, a copy of the user's configuration and a state directory of its own:
  `accounts` printed "week 50% · week, fable 74%" and "fable: not yet known whether the general limit caps it";
  `accounts reserve claude` added "kept for the lead: workers do not use it" and the warning that every Claude
  account is kept for the lead, which `doctor` reported too; `watch --once` showed the tightest limit per
  account and the LIMITS block; the interactive panel read both accounts in the background. These requests read
  limits; no model was started.

### Not done

- No run on a real model: the browser reviewer's new instruction about `UNVERIFIED`, and scenarios with
  `stage` written by a real preparation worker, are covered by scripted answers only. The tool no longer
  depends on the reviewer's status word: a pass that observed scenarios is recorded either way.
- Which of the two readings of Claude's limits is true is still not known: the first week in which a general
  limit runs out settles it on a real subscription.
- Two Claude subscriptions on one machine, one of them kept for the lead, were exercised only in the tests.

## 0.18.0

Checked on 2026-11-16 against the source based on `23bd9d1` (0.17.0); the plan is groups D and E of the
[brief](briefs/0.17-lead-experience.md).

### Automated checks

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| `go test -count=1 -json ./...` | PASS: 221 top-level tests, zero failures |
| Node core suite | PASS: 19 tests |
| `make design-browser`: Chromium workflow | PASS: 8 tests; the changed checker keeps its behaviour |

No permanent test was added: the brief limits new tests to one per defect of group A and scenarios of B1, B2,
B5 and B6. The new behaviour was checked once with throwaway tests that were deleted afterwards:

- Go, with the scripted model and a stand-in `figma frames`: a preparation that asks for a node
  (`design_requests`, depth 9) gets it read with `--node` at depth 6 and continues in the same session; which
  test files a declared check runs, over ten command shapes (a named file, `./...`, a package directory, an nx
  project, a bare runner, `true`, `sh -c`); a task whose writer adds a test no check runs names it in the
  answer and in the reviewer's packet; two designs of one page in one state are refused, `steps` or
  `compare: "review"` make them valid and reach the layout command.
- Chromium, through `check.mjs` with a local stand: steps bring the page to the state and the comparison
  passes; without them the root is missing (exit 3); a step that cannot be done is exit 3, and before the work
  it is recorded as not built; `--review` writes the pictures and measures nothing; before the work a redirect
  up the page's own path is "not built", a redirect to `/login` without a session is exit 2; the `session`
  command's cookie opens the protected page; a declared service and the stand are started and both are stopped
  after every check.

### Dry run

The harness of 0.17.0 with one scenario added: a writer changes two test files and the declared check runs one
of them. 0.18.0 held 102 of 102 promises; on 0.17.0 the new scenario holds 2 of 4 (no `unchecked_tests`). The
design changes need a browser and Figma and are not part of the dry run.

### Not done

- No run on a real model, and no real Figma file: what a real preparation worker asks for in
  `design_requests`, and the steps it writes, are covered only by the prompts.
- The checker's new paths were exercised once, not kept as a test.

## 0.17.0

Checked on 2026-11-16 against the source based on `e5ef01f` (0.16.2). What changed and why is in
[decision 0012](decisions/0012-the-lead-gives-work.md) and the [brief](briefs/0.17-lead-experience.md).

### Automated checks

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| `go test -count=1 -json ./...` | PASS: 221 top-level tests, zero failures |
| Node core suite | PASS: 19 tests |
| `make design-browser` | not run: the design kit did not change in this release |

Four scenario tests are new, one per action a lead was missing (`internal/dev/lead_actions_test.go`): work
given to the writer it names, with missing code going to its writer without the lead (B1); named operations of
`amend` (B2); a note of a finished task fixed in it (B5); `drop` that never overwrites someone else's edit, and
`forget` (B6). Existing tests changed with the protocol they describe: `decide` inputs became `work`; the
scripted reviewers give a `cause` for every unmet requirement; the fixture configures light mode with
`review: []`, which is how the lead's own review is configured now; the data of `land`, `show`, `stop` and a
review task's findings moved under `result` and `outcome`; substitutions at `done` are read from the receipt;
`install` also installs the lead's pages; a refusal names the valid finding ids.

The stop of a silent writer was checked once with a throwaway test of `ops.RunWatched` that was not kept: a
child that wrote nothing for the idle limit (1 s in the check) was stopped after 1 s, and a child that kept
writing ran to its end.

### Dry run: the real binary with recorded worker answers

The same harness as for 0.16.2: no model was called; the release binary ran with the real Bubblewrap sandbox,
git and worktrees, an isolated home and the user's own routes, and one stand-in for both CLIs answered with
answers recorded from real worker sessions. It was extended for this release: the stand-in records pictures
passed with `--image`, gives a reviewer's `cause`, writes the work of a correction, and reports a P2 note;
four scenarios are new.

| Scenario | 0.17.0 | 0.16.2, same script |
|---|---|---|
| A request that repeats `/dev -m --grill`, with a picture from `~/Downloads`: two preparation rounds, `start`, the receipt, the Stop hook, `install`, `reference needs` | 17 of 17 | 0 of 1, then stopped: `attachments` unknown |
| A blocked stream beside an independent one; `amend` adds the stream it waits for; `answer` | 14 of 14 | 5 of 7, then stopped: the need carries no input; the plan is not under `result` |
| A P1 finding, a correction that changed nothing, then code missing for a criterion | 4 of 4: the criterion went to its writer without the lead | 2 of 4: the lead is asked |
| Proof missing for a criterion: `show review`, an input of 0.16, `decide {work}` | 10 of 10 | 2 of 4, then stopped: no cause, no `show review` |
| A writer needs a file: a wrong stream, then `amend {scope, checks}`, `answer`, `status` | 11 of 11 | 4 of 6, then stopped: `scope` unknown |
| A P2 note at `done`, accepted by the lead | 6 of 6 | 2 of 4, then stopped: no `notes` |
| `drop` refused over the user's own edit, `drop`, `forget` | 10 of 10 | 4 of 6, then stopped: no `drop` |
| An isolated task whose check needs a local service, landed with a message | 20 of 20 | 16 of 20: the behaviour holds, the answers have the earlier shape |
| Two streams in one directory and a generated migration directory | 6 of 6 | 5 of 6: the behaviour holds, the answer has the earlier shape |

98 of 98 promises held on 0.17.0, in 7 seconds; the same script holds 40 of 58 on 0.16.2.

### Not done

- No run on a real model: the user asked not to spend model quota. What a real reviewer does with `cause`,
  `open_notes` and a picture, and what a real preparation worker names a plan, is covered only by the prompts
  and the stand-in.
- Of a running writer's progress, only its silence (`quiet_s`) was seen, in the answer of `stop` during a real
  sandboxed process in the test suite; the elapsed time and the last event in `status` were not checked.
- Whether the pages and the answers reduce a lead's searching is measured in the user's next working sessions
  of the same kind, from their transcripts.

## 0.16.2

Checked on 2026-11-16 against the source based on `b1112f7` (0.16.1). The defects come from the transcripts of
three Claude Code lead sessions and one Codex lead session of real work; the analysis is
[docs/briefs/0.17-lead-experience.md](briefs/0.17-lead-experience.md).

### Automated checks

| Check | Actual result |
|---|---|
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| `go test -count=1 -json ./...` | PASS: 217 top-level tests, zero failures |
| Node core suite | PASS: 19 tests (one new: an asset Figma does not render) |
| `make design-browser`: Chromium workflow | PASS: 8 tests (a missing root on an opened page now exits 3) |

Each defect has one scripted regression test in `internal/dev/lead_sessions_test.go`, written from the
session where it happened. Three existing tests changed with the behaviour they describe: a duplicate written
as `accepted`, the wording of a refused check reference, and a stand that is not up when a design stage opens,
which is now a `setup` need instead of a failed command.

### Dry run: the real binary with recorded worker answers

No model was called. The release binary ran as a lead's tool does: the real Bubblewrap sandbox, git and
worktrees, an isolated home, configuration and state, and the user's own routes (writers through the `codex`
CLI, reviewers through the `claude` CLI). Both CLIs were replaced on `PATH` by one stand-in that answers in each
CLI's output format and edits files as a writer, with answers recorded from real worker sessions — a writer
blocked on a missing dependency, a P1 review finding, a requirement judged `partial` after a correction that
changed nothing, a preparation question — adapted to a small repository. The lead's part was scripted from the
guide: the commands, their JSON inputs and a check of every answer. The harness and the recorded answers are
kept outside the repository, because the answers come from private projects.

| Scenario | 0.16.2 | 0.16.1, same script |
|---|---|---|
| A request that repeats `/dev -m --grill`: two preparation rounds, `start`, the Stop hook | 10 of 10 | 8 of 10: mode taken from the assessment, plan named `dev-grill-…` |
| A blocked stream beside an independent one; `amend` adds the stream it waits for; `answer` | 13 of 13 | 10 of 13: the answer reached the page before its query was written; the amendment's fields stayed in the plan |
| A P1 finding, a correction that changed nothing, a `partial` criterion, `decide {requirements}` | 6 of 6 | 4 of 6: the lead's work answered as `stall` |
| An isolated task whose check needs a local service: a correction that prepares it, the user's commit and a stopped service at landing, a refused `decide`, the Stop hook, the repair, `land` with a message | 17 of 17 | 3 of 6, then stopped: `stall` instead of running the check again; `land` has no `message` |
| Two streams in one directory and a generated migration directory, isolated, landed uncommitted | 6 of 6 | 0 of 1, then stopped: the plan was refused as overlapping |

52 of 52 promises held on 0.16.2; the same script found the defects of the sessions in 0.16.1. A layout check
in a real browser is covered by `make design-browser`, not by this run.

One preparation call on Codex had started before the user asked not to spend model quota; it was stopped
there and its temporary state removed. It confirmed that a request beginning with `/dev -m` is prepared in
medium mode, and showed the plan named after the command, which this version fixes.

## 0.16.1

Checked on 2026-11-14: `make check` — formatting, `go vet`, `go test -race ./...` and the 18 Node core tests —
PASS; the release workflow runs the same suites and the Chromium workflow again before it publishes. The two
fixes come from a lead's live session: its review was refused for a code reference written as a line range,
one problem per attempt. No new live runs were made for this version.

## 0.16.0 — the rewritten task core

Validated on 2026-11-14 against the source based on `29baaf9` (0.15.0). Automated suites, an archive check
and live runs on Codex were performed; what was not is listed at the end.

### Automated checks

| Check | Actual result |
|---|---|
| Host | Linux x86-64, Go 1.26.0, Node 24.18.0, Chrome for Testing from the installed Playwright |
| `make check`: `gofmt`, `go vet ./...`, `go test -race ./...`, Node core suite | PASS |
| `go test -race -count=1 -json ./...` | PASS: 203 top-level tests, 244 passing test events, zero failures or skips, no race reported |
| Node core suite (`make check`) | PASS: 18 tests |
| `make design-browser`: Chromium workflow | PASS: 8 tests |
| The scenario suites of `internal/dev` | 45 s under the race detector; whole tasks driven through the public operations with scripted workers |
| The command-line scenario | PASS: real `agentgate` binary, Bubblewrap, fake provider CLIs |

What the Go scenarios cover, all with scripted provider answers and no model quota:

- one command taking a clean task from writers to completion; a blocked writer answered in its session; a
  failed writer retried in the same round and never reviewed;
- a failing check sent to its author before any reviewer; a finding sent to its author, fixed and rechecked
  in the same sessions; a dispute withdrawn by the reviewer; a dispute upheld and ruled on by the lead; a
  correction that changed nothing stopping for a new diagnosis;
- the lead as the reviewer in light mode, with the same criteria and evidence rules as a worker, and with
  the author's dispute against the lead's own finding;
- a reviewer's unusable report corrected in its own session instead of discarded; an axis that owes something
  only because a criterion is unmet accepted as it is;
- a staged plan run to the end, step by step with `--step`, in a worktree with a commit per stage and a
  review of the whole change; a requirement due at the last stage that covers it;
- amendment of a reviewed stage without losing the task or weakening a requirement; a refused amendment
  leaving the task as it was; an amendment that only moves a requirement reassessed without a code review;
- before-images of files admitted late; generated output listed and left out of the patch; an edit that
  keeps a file's size in the second the index was written still making evidence stale;
- preparation continued in one session through answers, validation problems and a revision of a running
  task's plan;
- isolation: an untouched checkout until landing, catch-up with new commits including a conflict, a file
  outside the plan stopping the task before any review, landing that preserves the user's index and edits;
- design packages: evidence bound to package bytes, a stand failure never sent to a writer, a repaired stand
  measured again by `run`;
- ownership between sessions, corrupt state reported rather than read as "no task", a dead operation leaving
  no writer running;
- a task saved by 0.15: read without being rewritten, converted by its own session and run to the end.

Every answer produced in these scenarios is also held to the protocol's promise: each action it offers passes
the guard, an unfinished task that is not parked is offered an action that moves it, and every need names how
it is settled.

### Live runs on Codex

Five scenarios ran against real models through the normal Codex CLI (0.167.2), in a temporary repository
with temporary configuration and state. The numbers, what each showed and what was not exercised are in
[the live record](validation/live-0.16.json).

- **A staged task, driven by hand along the guide.** Preparation returned a typed three-stage plan that the
  validator accepted in the first round, in 59 seconds. Each stage was written, checked, reviewed and
  accepted by one command, in 92 to 128 seconds; the stage that changed the same files as the one before it
  was reviewed on its own change only. Two amendments were made on purpose: a requirement added between
  stages, and its method of proof changed after the reviewer judged it unproven. The second was reassessed in
  the reviewer's own session in 17 seconds, and the code review stood. The whole change was then reviewed
  once more and the task was done. The result builds, passes `go vet` and `go test`, and behaves as asked
  when run.
- **A Codex session as the lead.** The same small task was led by `codex exec` on both versions with the
  same model. On 0.15 the lead needed 14 commands and 757 k input tokens; on 0.16, 6 commands and 159 k.
  Workers used 557 k input tokens in four calls on 0.15 and 323 k in three on 0.16. This is one run each.
- **Questions.** A request that left two decisions open came back with two structured questions in 25
  seconds; the answers continued the same worker, which returned the plan in 20 seconds after reading one
  file.
- **A dispute in light mode.** The lead, as the reviewer, raised a finding that was wrong on purpose. The
  writer left the code alone and answered with the line that disproves it; the lead withdrew the finding.
- **A brief against a repository rule.** The writer stopped and asked before editing. After the answer, the
  reviewer raised a finding that breaks a boundary; it went to the writer without the lead; the writer
  disputed it with the lead's recorded answer; the reviewer withdrew it in its own session. One command.

The live runs found six defects that the scripted scenarios had not: a usable review discarded over the
status of an axis, a dispute the lead-reviewer was never shown, a dispute mistaken for a stalled correction,
a stage record that lost the work it recorded, an unnecessary parse step that the lead got wrong, and an
acceptance that did not say what was accepted. All six are fixed and covered by the suites above.

One of the two lead runs did something it should not have: the lead's login shell resolved `agentgate` to
the installed 0.15 binary, which ran the task in the real state directory instead of the temporary one. It
left one finished task there for the temporary repository; nothing else in that directory was touched.

### Migration

Copies of eleven real saved tasks of the earlier format (eight finished, one cancelled, two active) were
translated and validated in a temporary directory; the originals were only read. The active staged task was
upgraded against its repository on the copy: its plan resolves under the present rules, its origin trees
were built, and the panel, `status` and the Stop hook read the earlier format without writing. Details are in
[decision 0010](decisions/0010-state-format-and-migration.md). Two tests now hold this with one anonymized
task of the earlier format; the second of them exposed a defect in how the working tree is recorded
([decision 0004](decisions/0004-evidence-on-git-trees.md)).

### Distribution

`make package` built both Linux binaries and the source archive. The archive was extracted into a path with
a space: 223 files, every checksum in `SHA256SUMS` matches, and `.git`, `node_modules` and credentials are
absent. The offline installer installed the amd64 binary without integrations and it ran. Corrupting the
binary in the extracted archive made the installer refuse and left the installed executable in place. A
rebuild from the extracted source reproduced the shipped amd64 binary byte for byte. The arm64 binary was
cross-compiled and not executed.

### Not done for 0.16.0

- No run used Claude models as workers, and no Claude Code session led a task; the guide's instructions for
  that client were followed by hand in the first live scenario.
- No interactive Codex session led a task. In `codex exec` a command returns when it has ended, so the
  waiting rule of the Codex guide was not exercised; it rests on that client's recorded tool limits.
- Live: a writer fixing a reviewer's finding, an upheld dispute reaching the lead, a stalled correction,
  heavy and ultra, isolated worktrees, browser verification and design packages. The scripted scenarios
  cover each of them.
- The new binary was not installed over the user's installation, and no real task was converted.

Evidence: [Go test events](validation/go-tests-0.16.jsonl) · [Node core output](validation/node-core-0.16.log) ·
[browser output](validation/node-browser-0.16.log) · [live record](validation/live-0.16.json).

## 0.14.0 — release validation

Release validation on 2026-11-14 against the refactored source based on
`d1185635e89587683acfe90b047a29e0c70e7c72`, including the corrections found during this review.

### Environment and automated checks

| Component/check | Actual result |
|---|---|
| Host | Linux x86-64, Go 1.26.0, Node 24.18.0 |
| Local browser tooling | Installed Playwright core 1.65.0-alpha-2026-10-30; Chrome for Testing 157.0.8103.15 |
| CI browser dependency | Playwright core 1.65.1 remains pinned in package-lock.json |
| Formatting and `go vet ./...` | PASS |
| `go test -race -count=1 -json ./...` after fixes | PASS: 193 top-level tests, 230 passing test events, zero failures/skips |
| Node core suite | PASS: 18 tests |
| Full Chromium workflow | PASS: 6 top-level cases / 8 events, zero failures/skips |

The original imported validation record used a restricted environment where Chromium could not create a
socket. That is no longer an outstanding blocker: the current run launched a real browser. It then exposed
a test-helper defect: duplicate `--root` options were rejected by the newly strict parser. The helper now
supplies the default only when no override is present; the missing-root case asserts its actual diagnostic,
so an argument error cannot masquerade as the expected result.

Browser scenarios cover CSS colour conversion, artwork mapping, image dimensions, missing/changed roots,
baseline drift, mobile/tablet geometry, setup errors and cleanup/cancellation of a real temporary HTTP stand.
The Node core suite also exercises fixture identity, origin/path handling, atomic export, inventory integrity,
REST protocol errors and owned-process cleanup. These do not access a user's backend.

### Real provider-backed CLI smoke

A small isolated repository was processed through the normal Codex CLI (0.167.2): discovery, writer,
independent reviews, declared check, finish and read-only status/dashboard. The actual routes were Terra for
discovery, Astra:medium for writing and Sol:high for review. A local SKILL rule and check-backed requirement
were assessed, four assertions passed and the task reached `complete`. Only its assigned source file changed.

The repository, configuration/state/data directories and credential copies were temporary. Private credential
copies were removed after the run; user task state and installation were not changed. This is a real inference
smoke test, not evidence that models will always implement a large specification correctly.

### Real staged backend workflow

A second isolated fixture exercised a three-stage Go standard-library backend: query parsing/validation,
pagination business logic, then HTTP composition tested with `httptest` (no listening service or database).
Actual writer assignments carried the accepted prior-stage summaries and concrete handoff contracts.
Each stage passed independent review; finishing the third stage opened the required whole-change integration
review instead of immediately completing the task. That review verified all three requirements and the
local backend rule; all three declared Go checks passed and the task reached `complete`.

To limit token use, this fully specified fixture disabled discovery and used one independent combined review
lane, with `max_parallel=1`. It consumed eight live calls: three writers, three stage reviews, one recheck
and the final integration review. The recheck was necessary because the first review correctly marked
insufficient test evidence as `partial` and finish refused. Only the scratch verifier was expanded; the
contract and task state were not weakened. The cheap scripted scenarios below cover additional failures
without more inference calls. Temporary credential copies were removed afterward.

### Coordination, correction and recovery

The following use actual CLI processes, Bubblewrap and filesystem state, with scripted provider responses
so repeated protocol checks consume no model quota:

- Two backend streams with an `after` dependency: the downstream packet contains the upstream handoff.
- A BLOCKED consumer receives its answer in the original provider session; the completed upstream stream
  is not run again.
- A concrete P1 finding is accepted in triage, sent only to the owning writer, fixed in the same session
  and directory, then rechecked with the original finding ID and the correction delta. The task completes.
- A heavy plan retains one accepted whole-task architecture across two stages and a final integration
  review. Each stage is accepted separately and the overall task completes.
- After a failed writer, changing a project rule makes `continue`, repeated `launch` and `answer` refuse
  stale architecture before a writer starts. The saved batch and source baseline remain intact.
- Damaging a peer task's state cannot free its file claims: a second session is refused and the original
  source is unchanged. Normal disjoint sessions still work.
- Read-only `config show`, `validate`, `path`, `profiles` and `extensions` need no writable state. The actual
  Node `readScreen()` path through `config show` also works; absent state directories are not created.

The last three behaviours required production fixes during release validation. State decoding is shared
between current-session and ownership checks; continuation validates the whole Epic's architecture and the
current stage's scope/UI. No retry loop, state-file bypass or weaker acceptance was introduced.

### Live Figma browser export

Using a protected copy of the existing browser profile, the normal CLI successfully read a bounded catalogue
and exported one content frame through `--transport browser`: 73 layers, 12 assets and an 18-file inventory.
The node image is 1360×655, matching its geometry; the context image is 1920×1098. The Go `route` command
accepted the exported package's inventory. The profile copy was removed afterward. No live file was edited.

This validates the browser compatibility adapter against that file at the time of the run. The REST adapter
was tested with controlled responses, not a real token; access to other files or future Figma UI versions
is not implied. Private designs, profile data and URLs are not included in this report or the distribution.

### Distribution

The source-plus-binary archive was independently extracted. Its inventory/checksums and exclusions were
checked; `.git`, node_modules and private profiles are absent. Both Linux architectures were cross-compiled;
the amd64 executable was run. The offline installer succeeded into a path containing spaces. Corrupting its
input binary caused a checksum refusal and preserved the already installed executable. A source rebuild
from the extracted candidate matched the included amd64 bytes.

Final archives are rebuilt from the release source after corrections; the release workflow verifies the
extracted inventory and runs the offline installer again before publishing. ARM64 execution requires actual
ARM64 hardware and was not performed locally.

### Limits and reproduction

Live Claude inference, subscription exhaustion, every fallback model and physical-device/Safari testing
were not performed. Existing controlled tests cover provider events/failures, credential-refresh conflicts,
quota routing, worktree/user-change preservation and terminal input/rendering. The earlier PTY validation
is historical; this follow-up did not repeat that interactive session.

```sh
make check
make design-browser
make package VERSION=0.14.0
```

Use `PLAYWRIGHT_CORE` or `AGENTGATE_BROWSER_EXECUTABLE` when selecting already installed browser tooling.
CI installs its pinned dependency. Do not use tests to modify a live project's task state.

Evidence:

- [Current Go race events](validation/go-tests.jsonl)
- [Current browser output](validation/node-browser-current.log)
- [Current Node core output](validation/node-core-current.log)
- [Coordination and live-integration summary](validation/followup-scenarios.json)
- [Original restricted-browser attempt](validation/node-browser.tap)
- [Original archive/source checks](validation/archive-verification.log)
