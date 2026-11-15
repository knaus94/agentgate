# Validation record

This record distinguishes live integrations from controlled provider fixtures and does not claim exhaustive
model quality, arbitrary specification size or every possible execution environment. It has two parts: what
was run for the 0.16.0 source, and the release validation of 0.14.0, which is kept as it was written.

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
