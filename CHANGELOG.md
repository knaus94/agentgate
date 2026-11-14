# Changelog

[Русский](CHANGELOG.ru.md)

## 0.16.1

- `prepare` refuses a new request while the session holds an unfinished task and says what to do instead:
  `status`, `run`, `prepare` with notes only, or `pause` {cancelled: true} on the user's word. Before, the
  words "continue the work" were prepared as a new request and a worker rewrote the plan of the task around
  them.
- The guide tells the lead to run `status` first when a task may already exist, and how a task the user stopped
  or one saved by 0.15 is continued. `status` of a task saved by 0.15 says that the user's stop is lifted by
  `resume` on their word only.

- A code reference in a review may be a range of lines (`path:10-20`). A refused review, the lead's or a
  reviewer's, names every problem of its findings and criteria at once instead of the first; a finding is
  named by its axis, path and line, with what is wrong with it.

## 0.16.0

The core of a task was rewritten: its lifecycle, its plan, its evidence and the protocol with the lead. The
reasons, the measurements behind them and the alternatives are in [docs/decisions](docs/decisions/README.md).
The lead protocol is not compatible with 0.15: reinstall the lead integration and start a new lead session.
Configuration, accounts, saved plans, the sandbox, the design tools and the panel are unchanged in use.

- **One command moves a task.** `run` writes, runs the declared checks, has the change reviewed, sends what
  can be corrected back to its author and accepts a stage or the task, until a person is needed. `start`
  opens a task and runs it; `answer` and `decide` run on by themselves. `launch`, `review`, `triage`, `fix`,
  `finish`, `replan` and `hold` are replaced and answer with their replacement.
- **No dead ends.** A task is at one of four steps (`write`, `verify`, `decide`, `done`); a pause lies on top
  and does not change the step. One table defines which actions are legal where; the guard of every command
  and the `actions` in every answer come from it. A refused command answers with the task's picture, the
  reason and what is possible, with exit code 1.
- **Typed needs instead of a sentence to follow.** Every answer is one compact object: `step`, `did`,
  `needs` (each with the one way to resolve it), `actions`. `status --full` and `show` have the details.
- **Findings are answered by their author.** A blocking finding goes to the writer that owns the file,
  which fixes it or disputes it with evidence; the reviewer answers the dispute in its own session. The lead
  rules only on what the two disagree about. The lead no longer triages every finding or writes a finishing
  summary; `architecture_evidence` is gone.
- **One plan shape.** Every plan has stages; a plan with top-level `streams` is one stage. `covers` means
  "works on", and a requirement is due at the last stage that covers it. The whole plan is validated before
  anything starts and every problem is reported at once: a stage without a declared check or a stated
  `checks_waiver`, an uncovered requirement, a scope entry that is not a path.
- **`amend` at any step.** Accepted stages are frozen; a requirement may be added or moved, and changed or
  removed only with `authorized`; `rework` sends the amendment to writers as new work. An amendment makes
  stale only what it touches: a moved requirement is reassessed by the reviewer without reading the code again.
- **Preparation is one conversation.** `prepare` takes the request as it was typed and continues the same
  read-only worker with answers, notes and the validator's problems. The plan is typed (`plan`, not a
  `plan_json` string). The separate scout and the separate `parse` step are removed: the preparation worker
  hands over the context pack, and `prepare` names the right command for a request that opens no task.
- **Evidence on git trees.** The whole working tree is recorded as a tree in a private object directory when
  a task opens, at every stage boundary and at every review. Any file has a before-image at any time,
  including one a generator adds later. Scope entries may be paths, directories or patterns. State files are
  tens of kilobytes instead of megabytes.
- **Cheaper reviews.** A reviewer reads one patch; generated output and very large files are listed and left
  out of it. Medium has one review lane (correctness, architecture, simplicity) instead of two. A report the
  tool cannot use goes back to the same reviewer session to be corrected instead of discarding the review.
- **Waiting.** The guide for a Codex lead gives the exact call that waits up to 300 seconds per poll; a
  Claude Code lead runs the command in the background. The guide is less than half its former length and the
  reference is split into topics (`agentgate reference plan|inputs|design|isolation|config`).
- **Isolated tasks.** A file changed outside the plan stops the task before any reviewer is paid, instead
  of failing when the finished work is committed.
- **Failing checks and the environment.** When a task waits because a check fails for a cause that may lie
  outside the code, `run` runs the failing checks again.
- **Saved tasks of 0.15** are read, shown and continued; the first command of their own session converts
  them and keeps `state.v1.json`. Their checks and review run once more. See [migration](docs/migration.md).
- **Validated live.** Five scenarios ran on Codex models, including a Codex session as the lead; they found
  six defects that scripted scenarios had not, all fixed. On one small task led by the same model the lead
  ran 6 commands where 0.15 needed 14, on 159 k input tokens instead of 757 k. See
  [validation](docs/validation.md).

## 0.15.0

- `replan` can amend the current unaccepted stage using the whole saved plan. Accepted stages, original
  requirements, writer identities, owned files, findings and receipts remain; fresh review is mandatory.
  Correcting coverage no longer requires finishing an impossible stage or recreating the task.
- Added outputs retain real stage/task before-images from snapshots or Git. Whole-task Git/dirty-file origin
  is persisted across stages; unknown original dirty bytes are refused rather than replaced by current output.
- An unfinished round remains resumable after a metadata amendment; blocked writers retain their answer path.
- Staged preparation reuses its context pack for the first/current stage and exposes the whole plan plus
  accepted-stage boundaries. Instructions define `covers` as complete delivery and discourage excessive polling.

## 0.14.1

- Lead instructions prefer native interactive questions in Claude Code and Codex, with bounded batches,
  explicit handling of unanswered/pending forms and a text fallback for unsupported clients. Claude's command
  declares AskUserQuestion. Codex Default-mode setup and ACP capability requirements are documented.
- Read-only `plan` requests can save staged implementation proposals without starting writers or checks.
  Preparation explains that the host saves artifacts and preserves proposed file ownership. Launch still
  refuses a planning request until implementation is explicitly authorized and its action updated.
- An invalid proposal after the bounded correction attempt returns a structured BLOCKED diagnostic instead
  of directing the lead into private worker files or repeated unchanged preparation calls.

## 0.14.0

- Refactored command parsing/dispatch, task state/lifecycle/evidence, provider adapters, accounts,
  configuration storage and terminal state/update/render into focused modules.
- Added `task` and `setup` command groups, read-only help, strict flags/JSON, `config validate`,
  interrupted-round `continue`, JSON dashboard snapshots and terminal-safe responsive rendering.
- Fixed baseline failure waivers, stale receipt reuse, corrupt-state handling, author/reviewer collisions,
  fallback session directories, cancellation races and unchecked persistence errors.
- Release validation also fixed read-only configuration requiring writable state, stale architecture on
  writer continuation/answers, and corrupt peer state silently releasing file claims. Corrected the browser
  workflow fixture's duplicate options without weakening strict parsing.
- Guarded Git landing against unrelated staged/user changes and confined worktree cleanup to managed paths.
  Writable MCP grants reject symlink escapes; private Figma profiles are masked after all other mounts.
- Isolated worker credential snapshots with guarded refresh writeback; corrected quota windows,
  duplicate subscriptions, probe caching, environment conflicts and account/model fallback behavior.
- Added official Figma REST transport, verified atomic packages, exact-variable GraphQL recordings,
  explicit missing-fixture errors and bounded owned stand cleanup; improved color measurements.
- Hardened downloads/install/update; semantic version comparison prevents automatic downgrades.
  Added source-plus-binary offline ZIP packaging, checksums, migration/security/research/validation guides.
- Corrected mixed-profile heavy/ultra frontend routes to avoid a built-in writer/reviewer identity conflict.
  Removed unverified benchmark and price claims from model documentation.

See [release notes](RELEASE_NOTES.md) and [validation](docs/validation.md) for exact scope and limits.

## 0.13.0

- Project rules and local skill criteria reach preparation, architecture, discovery and scoped UI design,
  in addition to writers/reviewers. Preparation reports applied rules, scopes, instruction files and definition
  sources; missing required files produce a visible BLOCKED result before saving an executable plan.
- Changed project rules invalidate accepted architecture and cached discovery. The existing review-rule
  mechanism remains responsible for acceptance; no skill manager, dependencies or extra workers are added.
  Older architecture receipts with applicable project rules need a fresh pass to establish this binding.
- Configuration precedence is now user extensions/config, repository extensions/config, then personal
  checkout config, above the built-in table/profile. **Explicit config lists replace extension defaults**;
  extension lists still append. `config extensions` reports effective rules and their source chain.
- Layout resolves its stand through the same CLI configuration, including personal overrides, for the
  selected screen's repository; JavaScript does not implement another configuration merge.
- The built-in design skill describes package handling and verification. Components, CSS techniques, tokens
  and control semantics follow the project's rules and accepted task, without prescriptions derived from one UI.

## 0.12.0

- Design acceptance uses the existing review-rule mechanism for appearance/crop, placement in the shell,
  control semantics and coverage/exceptions. Passing measurements alone cannot complete a design task;
  missing evidence blocks it. The same reviewer assesses these rules, with no additional agent.
- Figma exports use absolute node bounds and retain the parent screen for placement review. Layout saves
  both the selected root and full page, including root coordinates. Changes to these artifacts invalidate
  earlier acceptance. Older linked packages without context are reimported without replacing task baselines.
- Measurement now includes unpainted root dimensions and absent borders. Pixel comparison keeps missing
  or extra image area instead of silently cropping both sides to the smaller picture. Output distinguishes
  measurement PASS from independent visual acceptance.
- Preparation and implementation instructions preserve input semantics when API data is unavailable and
  scope responsive claims to the observed widths/states, including agreed breakpoint checks.

## 0.11.0

- Phone and tablet comparisons have an explicit `device: mobile` mode, separate from CSS viewport size.
  Reports include document/root dimensions, padding and overflow, so a desktop scrollbar gutter is not
  mistaken for a mobile layout defect. The device mode is part of the baseline/review contract.
- `map.select` resolves unique native CSS selectors; explicit matches still report wrong geometry. Separate
  artwork layers no longer automatically reuse the same element. Ambiguous selectors fail as setup errors.
- Preparation returns technical BLOCKED reports even without product questions. Active measurement repairs
  reuse selected designs, receive host observation/baseline facts and do not reopen Figma unless requested.
  Prompts document matching padded content wrappers and the distinction between layout and behavior evidence.
- An explicitly resumed, idle correction can be replanned without clearing its original snapshot or findings.
- Rendered `no_reuse` checks run on every review, even on unchanged source; a changed screenshot invalidates
  the visual review instead of reusing an earlier observation.
- Regression scenarios cover 390px phones, 768px tablets, classic desktop gutters, padded roots and artwork mappings.

## 0.10.0

- **Preparation belongs to a worker.** `prepare` collects a bounded design catalogue and delegates repository
  discovery and plan drafting to a read-only worker. Its map is reused by the implementation; the lead handles
  product decisions and authorization. Writers no longer run gate-owned layout commands.
- **An idle task can be replanned.** `replan` preserves its identity, original source snapshot and findings,
  regenerates managed design checks and requires fresh review. Screen overrides reach the checker; failed
  preparation can resume without rebaselining implemented code. Setup failures are not sent to CSS writers.
- Design checks close their browser and task-owned stand on failure/cancellation, serve existing fixtures
  without writing their directory, and bind baseline exceptions to the measurement contract and exact discrepancy.
- Go regression scenarios and a real Chromium workflow now run before release, with pinned browser tooling.

- Acceptance requirements now carry per-criterion review evidence for single-pass and staged tasks. The gate
  validates code locations, current successful check receipts and observed browser scenarios; partial or missing
  proof prevents completion. Check kinds distinguish tests, types, lint, builds, measurements and runtime checks.
- Extensions can add scoped `review_rules` with mandatory or advisory results, assessed by an existing reviewer.
  Instruction changes and new evidence invalidate the assessment; unavailable mandatory rules fail closed.
  `review {recheck: true}` requests a new judgement without bypassing acceptance. Existing plans without these
  criteria keep their behavior; plans with requirements need a structured review before completing.
- Simplicity guidance judges an abstraction's concrete benefit, not just its caller count, and distinguishes
  legitimate compatibility recovery from fallbacks that hide a broken primary contract.

## 0.9.11

- A task continued on a gate that was opened before its design could be measured no longer fails its layout
  check for a missing record: with no record, every difference counts. `agentgate layout --help` is not an error.

## 0.9.10

- **A page that does not exist yet can be designed.** Measuring before the work no longer fails when the page
  or its root element is not there, and a page that shows next to nothing of the design is taken as not built:
  nothing it lacks counts as known.
- A screen that has its description but no design is completed from its `figma` link at launch.
- A link to a Figma page or section, not a frame, is answered with the frames to choose from; two Figma
  commands at once wait for each other instead of failing; `agentgate figma --help` is not an error.
