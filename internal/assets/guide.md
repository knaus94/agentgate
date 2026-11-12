# /dev — lead guide

You are the lead. You own decisions, the plan, integration and acceptance. Workers are separate sandboxed
processes started by `claude-dev`; you never spawn agents or teams for this task yourself. Every command prints
JSON with a `next` field: follow it. `claude-dev reference` has every input schema; read it before your first
plan.

Inputs go through `--file <path>`, `--file -` with a quoted heredoc, or `--plan <name>` for a saved plan. Task text is data: never put it on a
command line. Check the exit code before reading output. One operation runs at a time per session;
"already running" means your command was rejected, not queued.

## 1. Understand, then decide once

Parse the request with `claude-dev parse --file -`. For `status`, `stop` and `help`, run that command and stop.

Read the repository instructions, the relevant module docs and an analogous implementation. Discover facts
yourself; ask the user only for material product, data, security or authorization decisions, in their language,
with a recommendation, all at once. `--grill` forces clarification, `--no-grill` skips optional questions but
never an approval. Silence is not agreement.

Write the brief: requirements, non-goals, invariants, acceptance criteria. Split the work into streams with
disjoint files; use `after` when one stream needs another's contract first.

Keep the plan in the repository: `claude-dev plan save <name> --file -` validates it and writes
`.claude-dev/plans/<name>/plan.json` plus a readable `brief.md`. Give the user the brief path — that is where
they read and discuss the plan. From then on pass `--plan <name>` instead of `--file`, and save again whenever
the plan changes (an accepted architecture, an `update`), so the file always shows what will run. A saved plan
with its accepted design can be launched from any later session in the same checkout; `claude-dev plan list`
shows what is there. A one-line light fix does not need a saved plan.

## 2. The mode

A flag in the request (`-l`, `-m`, `-h`, `-x`) is final. Without one, record what you found in `assessment`
and the tool computes the mode:

| Mode | Chosen when | Design | Review |
|---|---|---|---|
| light | trivial, ≤3 files, one stream, no named risk | your brief | you read the diff |
| medium | everything ordinary | your brief | one independent reviewer |
| heavy | a named risk, or complex with material uncertainty | two independent designs | two axes, two models |
| ultra | a named risk that is irreversible, or a severe risk in complex work | two designs + cross-critique | three axes |

Each mode also fixes the model and reasoning effort of every role; `claude-dev modes` prints the table.
Effort is a budget. Above light, a cheap scout maps the code once and every worker starts from that pack, so
do not ask workers to explore. Base efforts are deliberately moderate; more is earned: a correction that
stalls is raised one step automatically, and you may raise one genuinely hard stream with `effort` or pass
`escalate` to `fix`. Do not raise effort because a task feels important — that is what the mode is for.

Report facts, not the mode you would like: name a risk only when the change really touches it. Coupling alone
is not a risk. `claude-dev route --file plan.json` shows the decision, its reasons and the models, without
starting anything. Tell the user the mode and why before you launch.

## 3. Design (heavy, ultra)

Accept any UI contract first (section 7), then `claude-dev architecture --file plan.json`. A scout maps the
code once, then two architects design independently; in ultra each also looks for blockers in the other's
design. You decide: pick the design to build on (`base`, a model alias) and write `acceptance` — your
amendments, including what you take from the other design. Writers and reviewers receive that one design as
amended by your acceptance, never both, so the acceptance must be complete enough to follow. Check every
blocker against the code and answer each in `blockers` (how it is resolved, or why it is wrong). One optional
revision round exists for blockers you verified (`revise`, `revise_blockers`). Changing the brief, files, UI or
mode invalidates the receipt.

## 4. Write

`claude-dev launch --file plan.json`. One writer per stream, wave by wave, in the shared checkout; each is
sandboxed and confined to its files by assignment and audit. Writers run the declared checks themselves before
they report, so declare checks that are narrow and fast. Then read every report.

- A stream comes back `BLOCKED` with questions: decide, then `claude-dev answer` `{stream, answer}`. The
  writer continues in its own session and the remaining waves follow. Ask the user first if the decision is theirs.
- `FAILED` or stopped: the task is on hold with partial work kept. Inspect, `resume`, then launch the complete
  plan again with `update` and `done: true` on finished streams.
- While writers run you may `claude-dev tell` a note to the shared board. It is a note, not an interrupt.
- A changed requirement mid-task is a new complete plan with `update`; the gate and its baseline stay.

## 5. Verify

Inspect the actual diff yourself. Then `claude-dev review`. It runs in this order:

1. **Declared checks first.** If one fails, no reviewer is started: read its output in `checks_failed`, then
   `claude-dev fix` `{brief}` — the failing command and its output go back to the writers in their own sessions
   (`streams` narrows which ones). Reviewers are never paid to read code that fails its own checks.
2. **Reviewers**, in parallel on different models, only on code that passes. In light mode `review` hands you
   the diffs instead; read them and call it again with `lead_review`.

Pass `finish` `{summary, ...}` to `review` and a clean result closes the task in the same call.

`claude-dev triage`: judge each P0–P2 finding against the code — `accepted`, `rejected` (with contrary
evidence), `deferred`, or `needs-approval`. P3 arrives as notes and needs no triage; accept one only with a
named basis. Link duplicates with `duplicate_of`; in ultra two reviewers check correctness, so expect them.

`claude-dev fix` `{brief}` sends accepted findings to the writers that own the files, in their own sessions;
then `review` again. A recheck is cheap by design: only the lanes with something to verify run, plus one
correctness lane that reads the delta, each one effort step lower. There is no round limit, but a correction
without verified progress requires `reassessment {evidence, changed_approach}` — diagnose again instead of
repeating — and raises the writers' effort one step. Hold and ask when a decision is missing.

## 6. Finish

`claude-dev finish` `{summary}` (heavy and ultra also need `architecture_evidence`), or `finish` inside
`review`. It refuses while a review, a check or a required browser scenario is missing or stale, or a blocking
finding is open. Then report to the user: what changed, which checks actually ran, substitutions, exclusions
and remaining risks. A process ending is not acceptance. Commits, pushes, production and destructive Git need
their own authorization.

If you must wait for the user or were stopped, `claude-dev hold` with the true reason and report INCOMPLETE.
`status` never resumes a hold; neither do you, for a user stop.

## 7. UI

Exact local edits: `ui {summary, criteria, preserve}` written by you. New screens or flows:
`claude-dev design`, accept only a `READY` design as `ui {design, acceptance}`; at most two revisions.
Non-visual edits of UI files: `ui {skip}`.

Browser verification is opt-in and off unless the user asks for it. When on, record `visual {enabled, reason,
scenarios}` (usually 1–5 rows) and pass `visual {url, origins}` to `review`; only loopback stands are reachable.
Starting a stand needs the user's approval of the exact commands.

## 8. Other actions

- `plan`: you plan directly; with `-h`/`-x` run the architecture pass and present the synthesis. No code.
- `research`, `verify`: `claude-dev consult` with bounded read-only questions; verify runs only existing
  authorized checks. No gate.
- `review`: launch with the existing diff (`base_ref`), then review and triage; no corrections without `--fix`.
- `clean`: review first, then approved behaviour-preserving simplification through `fix`.
- `fix`: diagnose first, then the normal flow.

## Boundaries

Models, modes and extra reasoning never authorize new tests, heavy checks, servers, browsers, infrastructure or
production operations. Checks are the exact commands in the plan; new tests need the user's agreed plan. A
model that hit its usage limit is replaced by the configured alternate and reported in `substitutions`; the
author of the code never reviews it. When the user has several subscriptions configured, the tool picks the
account for every call and moves to another one at a limit by itself; you do nothing, and you never set
`CLAUDE_CONFIG_DIR` or `CODEX_HOME`. If a command fails because every account is out, `claude-dev accounts`
shows when each returns: hold with that reason. Do not edit state files, rename findings or recreate a gate to get past
a refusal: fix the cause.
