# Architecture

This describes the implementation as of 0.16.0, whose task core was rewritten; the reasons are recorded in
the [design decisions](decisions/README.md). The product is a local process supervisor and evidence-based
workflow, not a new model runtime. Claude Code and Codex still own model inference and provider protocol
semantics.

## Boundaries

| Layer | Source | Responsibility |
|---|---|---|
| Command contract | `cmd/agentgate/args.go`, `help.go` | One registry of commands, flags, input sources and help; no actions while parsing help |
| Command dispatch | `cmd/agentgate/commands.go`, `design_tools.go` | Translate validated inputs into operations, serialize output, delegate standalone design commands |
| Position and legality | `internal/dev/machine.go` | The steps, the pause overlay and the one table of legal actions and transitions; the guard of every command |
| Plan | `plan.go`, `planschema.go`, `scope.go`, `plans.go` | One plan shape with stages, due requirements, validation of the whole plan, scope as paths and patterns, saved plans |
| State | `types.go`, `state.go`, `gate.go`, `migrate.go` | Task identity, persistence, corruption checks, what the present position owns, translation of earlier states |
| Driving a task | `autopilot.go`, `write.go`, `verify.go`, `decide.go`, `amend.go`, `control.go`, `run.go` | `run` and its classification, writer rounds and waves, verification, the lead's decisions, amendment, pause and stop |
| Preparation and design | `prepare.go`, `arch.go`, `ui.go`, `consult.go` | The resumable preparation worker, architecture passes, the interface specification, read-only consultations |
| Evidence | `tree.go`, `checks.go`, `check_receipts.go`, `review.go`, `quality.go`, `slop.go`, `visual.go` | Git trees of the working tree, check receipts, independent reviews, criteria, leftovers on added lines, browser observation |
| Answers to the lead | `summary.go` | The compact report, typed needs, `status` and `show` |
| Worktrees | `internal/dev/worktree.go` | Isolated checkout, preparation, scope audit, catch-up and guarded landing |
| Terminal UI | `panel_state.go`, `panel_model.go`, `panel_render.go`, `term.go`, `terminal_text.go` | Read-only dashboard data, navigation, rendering and terminal protocol |
| Provider adapters | `internal/provider/claude.go`, `codex.go`, `events.go` | CLI arguments, streamed events, structured results and resume identifiers |
| Provider execution | `sandbox.go`, `credentials.go`, `environment.go`, `routing.go`, `run.go` | Mount policy, private authentication snapshots, account/model routing and process results |
| Accounts | `internal/accounts/manager.go`, `limits.go`, `probe.go`, `codex_rpc.go` | Quota observations, cooldowns, bounded RPC lifecycle and account ranking |
| Configuration | `internal/config/load.go`, `validation.go`, `storage.go`, `profiles.go`, `extensions.go` | Layer precedence, structural validation, validated atomic edits and profile selection |
| Process and file primitives | `internal/ops/process.go`, `atomic.go`, `ops.go` | Cancellation, process identity, reaping, operation receipts and durable JSON replacement |
| Design toolkit | `design-kit/lib`, `design-kit/bin`, `design-kit/embed.go` | Figma transports, atomic packages, layout comparison, fixtures and stand ownership |
| Distribution | `internal/update`, `scripts`, `Makefile` | Checksummed atomic updates, offline install and source-plus-binary packaging |

The Go packages have no third-party dependencies. The terminal UI uses explicit state/update/render
separation without adding a framework dependency. Node and Playwright are optional for design work; basic
planning, writing, checking and text review do not depend on a browser.

## Data flow

```mermaid
flowchart TD
    Lead["Claude Code or Codex lead"] --> CLI["Validated CLI command"]
    CLI --> Guard["Guard: is the action legal at this step?"]
    Guard --> Gate["Task state and operation lock"]
    Gate --> Workers["Provider workers in bwrap"]
    Workers --> Receipts["Reports, sessions and file audit"]
    Receipts --> Checks["Declared checks"]
    Checks --> Review["Independent review and quality criteria"]
    Review --> Decision{"What follows from the evidence?"}
    Decision -->|Clean| Accept["Accept the stage: next stage, whole-change pass or completion receipt"]
    Decision -->|Fixable| Workers
    Decision -->|A person is needed| Needs["Stop at decide with typed needs"]
    Needs -->|answer, decide, amend| Gate
    Accept --> Gate
```

Every operation is a normal process. A session lock prevents two commands from mutating the same task
simultaneously; a checkout scope lock serializes file ownership claims between sessions. State lives in the
user state directory, separate from worker-writable code. No daemon needs to remain healthy between calls.

## Lifecycle and recovery

A task's position is a step named after what has to happen next: `write`, `verify`, `decide`, `done` (or
`cancelled`). A pause — by the user, for a decision, or between stages when the user asked to confirm each —
lies on top of the position and does not change it. `machine.go` holds one table of which actions are legal
at which step and which step may follow which. The guard of every command, the `actions` offered to the lead
and the checks in the test suite are all derived from that table, and `setStep` is the only assignment of the
step.

`run` reads the evidence of the present position and classifies it: stale evidence is verified again;
something a writer can correct opens a correction round; something only a person can settle becomes a typed
need; a clean position is accepted. The classification starts nothing and can be repeated safely, which is
what makes `run` idempotent.

A step alone is insufficient. Verification requires each scheduled stream of the current round to have a
completed record. Acceptance requires the current content and contract to match the review and all required
checks and criteria. Corrupt JSON or inconsistent state is reported; it cannot be treated as an empty
checkout with no active task.

An interrupted operation does not park the task: its running writers are recorded as failed and the next
`run` retries the unfinished streams while preserving completed reports and sessions. A blocked writer
requires an answer rather than a blind retry. A user stop is never automatically reversed.

Cancellation waits for process reaping before consumers read exit status. Process receipts include Linux
start identity so an old PID cannot identify an unrelated new process. Atomic state writes sync the file,
rename it and sync its parent directory. A failed persistence operation is returned to the caller.

## Evidence and invalidation

| Evidence | Bound to | Invalidated by |
|---|---|---|
| Code review | The exact change of the stage's files between two trees, the content of declared context, the code contract and the criteria | Changed scoped bytes, a changed contract; a changed criterion alone has the criteria reassessed without re-reading the code |
| Check with explicit `inputs` | Its command, declared patterns and the object ids git recorded for the matched files; files git ignores only under a pattern's literal prefix, read from disk; a link and its target | Input content/set changes, command changes, a link leading outside the repository |
| Check without `inputs` | The tree of the whole working tree it ran against | Any change of the working tree; a commit of the same bytes changes nothing |
| Before-image of any file | The tree recorded when the task opened and at each stage boundary | Nothing: a file admitted later is read from the same tree |
| Design package | Actual manifest and inventoried file bytes | Missing/corrupt inventory, changed spec/assets/images/brief |
| Design baseline | Design content, page/root, viewport, device, map and tolerances | Any contract change or a different discrepancy |
| Writer ownership | Assigned paths plus recorded attempts | Foreign changes, path escapes or incomplete streams |

Review freshness is scoped. An agent can read additional files outside declared context; those reads do
not automatically become dependencies. Declare meaningful context/check inputs and use an isolated
worktree when a stable task snapshot matters. Binding every review to every unrelated repository edit
would invalidate concurrent sessions unnecessarily, so it is not the default.

A pre-existing failing check remains a failing check. Baseline receipts explain history, but do not prove
that a later failure is the same defect. Layout baselines are narrower: an exact discrepancy under an
unchanged measurement contract can be recorded, and changed discrepancies still fail.

## Provider independence and account identity

Actual model identities of every writer attempt are retained, including partial failures, fallback attempts
and earlier stages. Reviewer routing excludes all of them. A different alias or another account of the same
model is not independent authorship. The mixed-provider defaults reserve Fable and Astra for review while
Opus writes substantial tasks; the Codex-only profile uses separate models in one provider.

Quota fallback is allowed only on a structured failure classified as a limit, never on successful prose.
The actual route, account, session and lane directory travel together. A continued session is not silently
moved to another model. The receipts expose substitutions and observed model/vendor diversity.

Credential files are copied to a private lane so provider CLIs can atomically refresh them. The parent
compares the original profile with the snapshot before writeback. Conflicting refreshes or a concurrent
login preserve the host profile and surface a conflict. This is narrower than making the user's entire
CLI profile writable inside a worker.

## Design subsystem

Figma REST and the browser scene-graph compatibility adapter both produce the same design package. The
REST adapter requests documented file/image endpoints and pins render requests to the loaded version.
It sends the token only to the API host. Browser access uses the user's own separate profile and checks
export permission; it is subject to Figma's private UI changes.

An export builds in a sibling temporary directory under an exclusive export lock. Only a complete package
with a verified inventory replaces the previous package. Failed renders/downloads leave the previous
package intact. The Go gate and Node checker both verify inventory integrity.

A layout run owns only the stand and fixture processes it starts. A healthy existing stand is reused and
left running; an unhealthy existing listener is reported. Owned processes get bounded readiness and
termination handling. `--keep` explicitly detaches a successfully started stand and prints process/log
information. The fixture server binds to loopback, rejects unsafe names and oversized requests, and does
not invent successful data for an unrecorded operation.

## Compatibility decisions

The flat command API remains available alongside `task` and `setup`. Existing valid configuration and
exported packages continue to work. The lead protocol changed in 0.16: the commands of the earlier protocol
answer with the name of what replaced them, and a task saved by an earlier version is read, shown and
continued ([state format and migration](decisions/0010-state-format-and-migration.md)). Unknown flags, malformed values, incomplete packages and invalid state
that previously slipped through are now errors. Heavy/ultra frontend defaults changed to prevent a built-in
writer/reviewer collision. See [migration](migration.md) for behavior changes and recovery commands.
