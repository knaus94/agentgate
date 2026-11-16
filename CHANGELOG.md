# Changelog

[Русский](CHANGELOG.ru.md)

## 0.20.2

The built-in table divides the work by what each model family is for: Claude models design and review, Codex
models plan, write and check.

**Changed**

- Preparation and consultations run on `astra:medium`; every writer is a Codex model: Sol in light and medium,
  Astra in heavy and ultra.
- Light gets an architect, `opus:medium`, and its reviewer becomes `opus:medium`. The UI designer is Opus in
  every mode.
- Heavy: architects `opus:high` ∥ `astra:xhigh`, closed by Opus; review lanes correctness `fable:high` ∥
  architecture and simplicity `opus:high`.
- Ultra: architects `fable:high` ∥ `astra:max` with cross-critique, closed by Fable; review lanes correctness
  `fable:xhigh` ∥ correctness `opus:xhigh` ∥ security `opus:high` ∥ architecture and simplicity `fable:high`.
- Fallbacks: Astra, Sol and Fable fall back to Opus at the same effort. Opus has none: when it is exhausted
  the call waits rather than letting Codex review.
- The design pass may take 30 minutes in heavy and 60 in ultra.
- The Codex profile sets its UI designer per mode, so it keeps Astra there.

## 0.20.1

The lead is reminded whose the design is at the moment the user speaks, not only in a guide it read long ago.

**Added**

- A hook on every message of the user (`UserPromptSubmit`), installed beside the Stop hook for Claude Code and
  for Codex. While the session has a task it adds a few lines for the lead: a doubt about how the change is
  built goes to the architect of the accepted design, with the command and the receipt — or to the preparation
  worker in a mode without architects. After the task: the next request goes to `prepare` as the user's own
  words. It reads the saved task and changes nothing; a session without a task is told nothing.

**Changed**

- `agentgate install` and `update` add that hook next to the hooks you already have; `doctor` checks both as
  "lead hooks". In Codex, review and trust the new entry once with `/hooks`.
- A failure inside a hook answers nothing instead of exiting with the code a client reads as "block".

**Why**

In a recorded session the lead read the guide's rule against filtering answers and then filtered every one of
its next 46 commands. A rule read at the start of a session is far away, or compacted out, when it is needed.

## 0.20.0

The design of a task has an owner. A lead session was found designing the change together with the user: it
wrote a solution into the request, answered the architect's questions itself, and added whole designs to the
plan as requirements. The tool had handed it the design in five places. Now the architect decides how the
change is built, the user approves, and the lead carries.

**Changed**

- **The architect asks the user.** A design returns `questions` in the shape the preparation uses — options and
  a recommendation — and is built on the recommended options. `start` and `amend` refuse a design while a
  question has no answer in `architecture.answers`, and one whose answer differs from the recommendation until
  the architect has reworked it. Before, `open_questions` were plain strings that nothing took to the user.
- **Two architects end in one design.** In heavy and ultra the first architect closes the pass in its own
  session: it reads the other design, answers every blocker of ultra's critique and returns the design the
  task follows. The lead no longer chooses a `base` or verifies blockers against the code; only a blocker the
  closing design rejects needs the user's decision in `architecture.blockers`. This is one more model call per
  pass.
- **`architecture.acceptance` is optional** and holds what else the user decided about the design, in their
  words — not the lead's amendments. `architecture.id` accepts. Writers and reviewers receive the design with
  `answers` and `acceptance` (was `lead_acceptance`).
- **`revise` and `revise_blockers` in a plan are refused**: a revision is its own input, below.
- **A design is no longer bound to the files of a one-stage plan.** A file a stream gains or loses does not
  ask for the design again; a receipt of 0.19 stays valid for the files it was made with.
- **The architect is told what the preparation found**: the plan's `assessment`, with its named risks and the
  load. Its instructions say that the design covers the existing code the change is built on, that every named
  risk and the load are answered, and `alternatives` lists what was weighed and why it lost.
- **The guide: the design is not the lead's.** The request is the user's words, quoted, never the lead's
  summary of a solution; `notes` are hints the preparation worker checks. A worker's question goes to the user
  with the worker's options and recommendation. A doubt about how the change is built goes to the architect. A
  requirement says what must be true, never how.

**Added**

- `agentgate architecture` with `{revise, answers, notes, plan}`: the architect of a design continues its
  session with the user's answers and with what the user said about the design — before a task and in the
  middle of one, any number of times. It returns a new receipt with the design as it stands, or the evidence
  that the design already holds, and `plan_changes`: what the plan needs.
- `amend` takes the named operation `architecture`: the receipt the task follows from now on.
- `assessment.load`: for a path whose cost grows with data or with how often it runs — what grows and how
  large, how often, the time allowed, and the source of each number. It does not change the mode. Writers and
  reviewers read it in the brief, and the review lane that judges correctness also reports the new axis
  `performance`.

**On upgrade**

A task in flight keeps its accepted design: a receipt of 0.19 is read as before, with `base` and an answer for
every blocker. Its present stage is reviewed once more, because the packet of writers and reviewers changed.

## 0.19.2

A lead that loses the answer of `prepare` no longer has to run the preparation worker again.

**Fixed**

- `status` shows the answer of a preparation that waits on the lead (`BLOCKED`: questions for the user or a
  prerequisite) under `preparation`, with its `next`, also when an earlier task of the session is done. Before,
  it showed only the finished task and its "Done", and the questions could be read only by asking the worker
  to repeat them.
- The guide says that `prepare` and the other commands before a task answer without `result`, and that an
  answer is read whole: a lead filtered the answer of `prepare` for `.result.questions`, got `null` and lost
  the questions.

## 0.19.1

The model table the user settled on becomes the built-in one, and the rule that refused a task when a reviewer
would be the model that writes is gone.

**Changed**

- Every role runs on the model the table names, also when that model writes the code: a review lane, the
  browser check and a reassessment are no longer moved to a fallback or refused because their model wrote part
  of the task. `reviewers_excluded` is the only exclusion. Two architects on one model are no longer refused.
- The built-in table: light writes `sol:high`, reviewed by `astra:medium`; medium has an architect
  `opus:high`, writes `sol:xhigh`, reviewed by `opus:high`; heavy writes the backend with `opus:high` and the
  frontend with `astra:high`, reviewed by `fable:high` and `opus:high`; ultra writes with `opus:xhigh` and
  `astra:xhigh`, reviewed by `fable:high`, `fable:xhigh`, `opus:xhigh` and `opus:high`. The browser check is
  `astra:high` in every mode. The effort ceiling of light and medium is `xhigh`.
- The built-in table declares no fallbacks: an exhausted limit stops the call until it returns.
- Medium has an architecture pass by default: `agentgate architecture` before `start`.

**Added**

- `modes.<mode>.ui_designer`: the UI designer per mode — `astra:medium` in light and medium, `astra:high` in
  heavy, `astra:xhigh` in ultra. `agentgate design` takes `mode`; `agentgate modes` shows the designer, the
  browser check and the consultant.

## 0.19.0

An account for the lead, the limits a model has of its own, and what the sessions on 0.18 showed: the tool's
own load on the machine, browser evidence it threw away, refusals that left the lead no legal move. The
configuration of an account, the saved accounts and the task's state gain fields; nothing is removed. Receipts
of checks with declared inputs are bound differently: those checks run once more on the first verification.

**Added**

- `lead_only` on an account keeps it for the lead session: no worker runs on it, nor on another directory
  logged in to the same subscription, also when every other account is exhausted — the worker goes to its
  model's fallback. `agentgate accounts add … --lead-only`, `accounts reserve <name>` and
  `accounts unreserve <name>` set it. `accounts` and `doctor` warn when every account of a provider that
  workers are routed to is kept for the lead. A configuration with the field is not read by 0.18 and earlier.
- `watch` shows LIMITS: per account its provider, name, plan and every limit by name (`all`, a model's name,
  `5h`, `week`), the reset of a limit at 80% or more, and what the tool does with the account. `L` hides the
  block; on a small terminal it gives way to the tasks. While the panel is open it reads again the limits of
  an account whose reading is older than five minutes, one account at a time, in the background; it starts no
  model. `--once` and `--json` print what is saved.
- `watch --json` carries `accounts`: the configured accounts with their saved readings.
- A running operation says what it is busy with: `status` under `running.now`, the panel under the task's
  row, `watch --json` under `now`. Each line of `did` that names a phase ends with its duration.
- The first answer after agentgate changed under an open task carries `notice`: read `agentgate guide` again.
  The Stop hook repeats it.
- Browser scenarios take `stage`: a scenario is due at the stage it names, at the last stage when it names
  none, and in the pass over the whole change.
- `config validate` and `modes` warn about a mode whose browser verification no configured route could run.

**Changed**

- Declared check inputs are read from the tree git records instead of a walk of the disk: what git ignores
  — a tool's cache under `node_modules`, a build output — is an input only where a pattern points into it by
  its literal prefix; a link counts with the files it leads to inside the repository. `status` and `show` on
  a large repository took 7 s and now take a fraction of a second; a check whose own cache sat among its
  inputs no longer invalidates itself.
- A reading command takes one record of the working tree instead of one per part of its answer.
- The panel reads a file again only when it changed and reads a worker's events on from where it stopped. It
  judges the evidence on a change of state, on `r`, and every 30 seconds for an open task while no operation
  runs: an open panel took 65% of a core on a running task and takes under 1%.
- A browser pass that observed its scenarios is recorded even when the reviewer could not establish
  everything: what it could not establish is kept beside it, in `did`, `show receipt` and the final report.
  Only a pass that observed nothing fails.
- A substitution says why the route was not used: its model wrote the code under review, no account has room
  (and when one is back), or every account is kept for the lead. It said "usage limit" for every cause.
- An opted-in browser verification whose reviewer and every fallback are the writing model is refused at
  `start` and at `visual {choice}`, before the work.
- The pass over the whole change takes additions to accepted stages: more files for a stream (`scope`, with
  the `stage.stream` names it uses), a new requirement, a check.
- The declared inputs of every check are looked at when the plan is accepted: a link leading outside the
  repository is refused at `start` and `amend`; a pattern that matches nothing yet is said.
- The need for failing checks names each one with its verdict line — for a layout check its
  `layout measurements:` line — and the end of each log.
- The Stop hook says that no command is running and how the last one ended, and frees the session before it
  reads what the task needs: a command the lead starts meanwhile is no longer refused as "already running".
- Whether the general weekly limit of a Claude subscription also caps a model that has a limit of its own
  (Fable) is learned once per subscription instead of assumed. Until it is known the model's room is the worse
  of the two limits, and at an exhausted general limit with the model's own limit free the call is tried. A
  success records that the limits are separate: only the model's own counts from then on. A refusal for a
  limit records that the general one caps the model and rests it until the general limit resets. `accounts`
  says which was seen; `accounts reset` forgets it.
- A refusal for a limit rests what the exhausted limit covers: a model's own limit rests that model, the
  general limit rests the models without a limit of their own. Before, the general limit rested the whole
  account, a model with free room of its own included.
- A limit records its period, `session` or `week`, in `accounts --json` and the saved state.
- The header of `watch` names each account's tightest limit ("main fable 72%") instead of a bare percentage.
- The error "no account has room" names the accounts kept for the lead and when an account is back.

**Fixed**

- `agentgate figma node` failed in a writer's sandbox on the design kit's lock file; a complete kit is now
  used without writing.
- A sandboxed writer could not build Go: it gets a `GOCACHE` under the task.

## 0.18.0

The quality of the result and the design work: the last part of the plan in
[docs/briefs/0.17-lead-experience.md](docs/briefs/0.17-lead-experience.md). The plan and `.agentgate.json`
gain fields; nothing is removed.

**Added**

- Designs of one page that differ by state carry `steps` (`click`, `fill` with `value`, `wait`, each a CSS
  selector) run before measuring, or `compare: "review"` for a state judged from its pictures only. Two
  entries that would measure one page in one state are refused. `agentgate layout` takes `--steps <json>`
  and `--review`.
- `design.stand.services` (each `{command, ready}`) start before the front end and stop after the check, or are
  reused when already healthy; `design.stand.session` (`{command}`) prints the cookies of a signed-in user as
  JSON for every check.
- A preparation worker that needs more of a Figma file returns `design_requests: [{node, depth}]`; the tool
  reads the nodes and continues the same conversation, up to three times per call.
- Test files the task added or changed that no declared check runs are named under `unchecked_tests`, in the
  answer and in the reviewer's packet.

**Changed**

- When a design stage opens, the stand is started and every design's page is opened before the writers
  start: a page that is not built yet is fine, and so is one that sends the browser up its own path, but a
  redirect elsewhere — usually to a login — is a `setup` need then, not after the work. A step that cannot be
  done on a page that opened where it was asked to is a defect of the code (exit 3), for the writer.
- The preparation worker plans the tests that reference what the plan changes and a typecheck of every project
  whose shared types change, proposing the heavy ones as authorizations.
- The reviewer is told what verifies a requirement proven by a check — a current passing declared check that
  exercises the changed code — and that a wish for more proof is not a gap.

## 0.17.0

What a lead was missing in real sessions, and the answers it had to work around. The lead protocol changes:
`decide` takes three forms instead of seven, and every answer has one shape. Decisions:
[0012](docs/decisions/0012-the-lead-gives-work.md); plan: [docs/briefs/0.17-lead-experience.md](docs/briefs/0.17-lead-experience.md).

**Changed — the lead protocol**

- `decide` takes `findings`, `review` and `work: [{stream, note, requirement?, escalate?}]`. Work opens a
  correction for the named streams at once, in their writers' sessions, at any step after their round and at
  `done`; failing checks go to the streams named. `requirements`, `reassessment`, `brief`, `streams` and
  `escalate` are removed; sending one is refused with what replaced it. A diagnosis given after a stalled
  correction raises its writers one step of effort by itself.
- A requirement that is not verified carries the reviewer's `cause`: `code`, `proof` or `plan`. Missing code
  goes to the writer that owns it without the lead, within the limit of automatic rounds, and is closed when
  the criterion is verified; missing proof and a wrong plan go to the lead.
- Every command about a task answers with the common object; its own data is under `result`. A refused
  command carries `refused`, a failed one `error`, both with the task as it is (exit 1). `status` adds
  `running {action, since}` and `last {action, state, error}`. A review task's findings are under `outcome`.
- Each need carries `command` and `input`: the call that settles it, with the real ids, streams and valid
  references filled in. Its `what` begins with the cause in the tool's own words; long details are clipped. A
  refusal that depends on a value lists the valid ones.
- Light mode has a reviewer, `astra:medium`. The lead reviews only when a configuration sets `review: []`.

**Added**

- `amend` takes named operations beside the complete plan: `scope`, `checks`, `requirements`, `timeouts`,
  `design` and `mode`, each with `update`, validated like a complete plan.
- `mode` in the plan and in the input of `prepare`, stronger than a flag. When a flag or the plan sets a mode
  below what the assessment gives, `because` says so with the risks.
- `prepare {attachments: [paths]}`: files anywhere on the machine are copied, shown to the preparation worker
  (pictures as pictures) and carried into the task for its writers and reviewers.
- A finished task lists its notes in one line each; `decide {findings}` on a finished, unlanded task reopens it
  where it ended with those findings accepted.
- `drop`, on the user's word, ends a task and takes back what it changed; a file someone changed after the
  task's writers left it is never overwritten. `forget` removes a finished or dropped task from the record.
- At `done` the answer carries `receipt`: routes per role, substitutions, check results, notes, rounds and
  time per stage. `show receipt`, `show review` and `show architecture` print what a person judges and the
  input to answer with; `architecture` answers in the same readable form.
- `status` shows a running writer's elapsed time, its silence and its last event.
- The lead's pages are written by situation (`needs`, `answers`, `scope`, `mode`, `models`, `design`,
  `finished`, `places`, `refusals`, …) in one place: `agentgate install` installs them as the skill
  `agentgate` for both clients, and `agentgate reference <page>` prints the same text. `guide` is shorter.
- The preparation worker names the plan; a name derived from the request ignores links.

**Fixed**

- A writer is stopped after ten minutes without an event, not by a twenty-minute clock; `writer_timeout_s` is a
  ceiling, 3600 seconds by default. A stopped writer is continued in its own session by the next `run`.
- A later review pass is shown its open notes, and a finding raised again at the same path and line joins the
  earlier one instead of appearing under a new id.
- The brief of a saved plan quotes the first lines of a long request instead of all of it.

## 0.16.2

Defects real lead sessions of 0.16 ran into; the lead protocol is unchanged. The analysis and the plan for the
next releases are in [docs/briefs/0.17-lead-experience.md](docs/briefs/0.17-lead-experience.md).

- A request that repeats the command (`/dev -m …`, `$dev -h …`) keeps its flags; before, the mode fell back to
  the assessment, which chose ultra for a `-m` request. `--plan`, `--review`, `--research`, `--clean` and
  `--verify` are accepted as the action words.
- Stream scopes overlap only when one path can belong to both: `pages/x/api/**` and `pages/x/*` are disjoint.
  An entry that names a directory owns what is inside it (`migrations/*_orders` owns the `migration.sql` a
  generator writes there), and a pattern written with a trailing slash owns everything below it. `dir/**`
  names the contents of `dir`, not `dir` itself.
- Work the lead sends to a writer (`decide {requirements}`) is never read as a stalled correction.
- A failing check is run again after the correction that was sent to repair it, also when no file changed.
  A check that passed on the same files of the task and fails now goes to the lead as `setup`, not to the
  writers; `run` runs it again once the cause is repaired.
- A stream that waits for an answer holds back only the streams that wait for it: the others run and `run`
  stays available. `answer` runs the streams the answered one depends on first.
- A design screen the tool makes is placed under the screens directory even when the plan gave a bare name;
  its package is not reported as a change outside the plan.
- A design that cannot be imported or measured is a `setup` need that names the exporter's own error, instead
  of a failed command that pointed at an error nobody saw. An icon Figma does not render is listed as missing
  in the package and does not stop the import.
- A layout check whose page opened where it was asked to and does not show the design's element exits 3 and
  goes to the writer with the `data-*` attributes the page carries. A page that did not open or ended at
  another path (a redirect to a login) is the lead's `setup`, named by the tool's message.
- The last stage of a staged plan runs every declared check, so a requirement due there is proven without
  copying checks into it.
- `land {how: "commit"}` needs `message`: the lead writes it in the repository's convention.
- Check evidence may name the check by a part of its command (`client:test`); a refusal lists the checks that
  can be named. A finding marked `duplicate_of` is closed as a rejected duplicate whatever its decision says.
- `show plan` no longer returns the last amendment's `update`, `authorized` and `rework`; they are kept in its
  revision, so a plan read back and amended again does not repeat that rework.
- The Stop hook says when the lead's last command was refused, and what the task needs.

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
