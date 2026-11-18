# 0013 — The session writes; a mode is the shape of the process

## Context

Through 0.21 a mode was one row of a table: a model and an effort for each role, the number of review lanes
and the severities that block. The process was the same in every row — a preparation worker planned, an
architect designed, a sandboxed writer per stream wrote, reviewers read every stage, findings went back to the
writer — so a higher mode named another model or another effort and added readers. From heavy to ultra the
writer was the same model one effort step up, with the same packet.

Measured on 35 archived stages of two repositories and the lead sessions behind them (stage archives, the
workers' `runs.jsonl` and session files, the leads' own transcripts):

| | |
|---|---|
| Review findings | 72: 0 P0, 2 P1, 70 P2 |
| Heavy stages that needed a correction round | 18 of 19; in 14 the worst finding sent back was P2 |
| Medium stages that needed one | 5 of 16 |
| Writer runs, heavy | 19 first, 28 continued after `BLOCKED`, 37 correction rounds, 4 after a failure |
| Writer runs, medium | 18 first, 11 continued after `BLOCKED`, 13 correction rounds, 3 after a failure |
| Reviewers' share of a heavy task | 58–60% of fresh input tokens, 50–67% of output; about a third with cache reads |
| What stopped a writer (45 blockers) | 24 an environment the sandbox could not reach or prepare, 11 a file outside its stream, 3 a check only the lead could change, 7 a real question |

- The findings were real, and of a kind: 22 behaviour defects, 21 pieces of unneeded or leftover code, 13 that
  duplicated or bypassed what the project already had, 4 undeclared departures from the design or the scope.
  They were classed from the first 230 characters of their evidence by one reader.
- Every hand-off dropped context: user → lead → preparation → architect → writer → reviewer. The writer had no
  conversation, no MCP servers, no skills, a fixed scope and a read-only host.
- The one participant that held everything — the conversation, the user's decisions, the whole repository —
  was forbidden to read the code or to design. In the session behind 0.20 it designed anyway: 43 amendments
  and about 80 direct reads of the code. 0.20 forbade the symptom.
- The stage was both the unit of work and the unit of review, so the small stages the user asks for multiplied
  the review bill.
- Risk was a fact of the task. A plan of 24 stages that named its seven risks once ran every stage as heavy,
  and a refactoring that moved the files of a wallet was computed as ultra; the user lowered it during its
  design pass.
- `--grill` was parsed and read by no other code. Light started four model processes for "an exact change of
  at most three files".

No ultra task was measured. No claim is made about tokens: a long session that writes re-reads its own
context on every turn (one session of Opus with the client's advisor read 332 M cached tokens for 63 edits
in 24 files, a task not comparable in size). What changes is counted in model runs and round trips, and the
receipt of every task now measures the rest.

## Decision

**The lead's own session writes the task.** It asks the user, reads the repository, plans and writes, stage
by stage, in the checkout the user is looking at. The gate is kept and the author changes: `classify`,
receipts bound to trees, findings with stable ids, requirements and `amend` judge trees, not authors. The
pipeline of sandboxed writers is unchanged behind `--workers` and `executor: workers`; a task keeps the
executor it was started with.

**The roles invert; the machinery stays.** The pass that produced a design challenges one: the advisor — a
route the configuration names, which is shown the conversation — reads the session's plan, and in heavy and
ultra critics that were not shown the conversation read the session's design, in ultra after designing the
change themselves. The session answers every blocker; one it rejects goes to the user with both arguments.

**A mode is the shape of the process**, not a row of models: how far the questions go, what the plan must
contain, and which kind of second look the work gets, and where.

| | light | medium | heavy | ultra |
|---|---|---|---|---|
| The plan is read by | nobody | the advisor | the advisor and critics | the same; critics design blind first |
| The code is read by | the session, with a proof for every requirement | the advisor | independent lanes | the ultra lanes |
| Model runs besides the session | 0 | `2 + C` | `1 + K + L × (C + 1)` | the same, with a second round for every critic |

`C` is the checkpoints before the last stage, `K` the critics, `L` the mode's lanes. The mode is a flag in
the request, or medium. The plan's assessment never raises it: `plan save` says what the facts suggest, and
the user, who decides what a task may spend, is told. The price is in the brief before the user approves.

**Three units instead of one.** A *stage* is a small outcome that leaves its checks green; closing it costs no
model run. A *checkpoint* is a stage later stages build on — the plan marks it, the advisor challenges the
marks, the price counts them — and there the mode's reader reads everything since the last reading; after the
last stage the whole change is read. A *risk* belongs to the stage that changes the risky behaviour: in heavy
and ultra such a stage is a checkpoint without the mark. A review only at the end would bring back the one
huge diff stages exist to avoid; a review at every stage is the measurement above. Checkpoints are placed by
what depends on what, not by the size of a diff, which [0011](0011-kept-and-rejected.md) rejected for having
thresholds nobody could justify.

**The plan says what the code follows.** Above light every stage names the existing code it follows (`like`)
or says why nothing comparable exists (`like_waiver`: a new pattern the user sees before it is written), and
the plan carries the session's map of the area (`grounding`). Thirteen findings were "the project already has
this"; nothing in the earlier process asked whoever planned to look.

**The user's words reach a second model as a digest**: the user's messages as they wrote them, what the
session said, and one line per tool call — no tool results and no reasoning, which hold file contents and
whatever a connected service returned. It goes only to a route of the lead's own provider.

**What only a rule can keep is kept by a refusal or a hook**, not by the guide (0.20.1): a plan above light
does not start without its reading and the user's approval; only the open stage can be closed; a file changed
outside the plan — also one written between the request and the start — is settled before anything is read.

## Alternatives

- **Keep the pipeline and feed it better** — a stronger preparation model, the digest for writers. 38 of 45
  blockers came from the sandbox, the fixed scope or the check configuration, not from the brief, and the lead
  still spent a frontier session on carrying.
- **The session plans, workers write.** It keeps the writer's hand-off, where the resumed runs are. It remains
  available: `--workers`.
- **The client's own sub-agents as writers.** The same hand-off without the sandbox or the receipts.
- **No tool: a skill that tells the session to plan and call an advisor.** A lead read a rule and broke it in
  its next 46 commands. Nothing would bind a check to the files it ran against, and the second look would
  depend on a feature of one client.
- **Fork the lead's session as the advisor.** Both clients can fork a session, and a fork holds the whole
  context. It cannot cross providers, and a digest can be tested with fixtures.
- **Blocking only P0 and P1 everywhere.** 70 of 72 findings were P2 and most were real; what was expensive was
  the loop they travelled, not the rule.
- **An independent reviewer in medium wherever a stage names a risk.** Spending decided by a tag is what the
  user stopped. In medium a risk tells the advisor where to look.

## Consequences

- Superseded for a task the session writes: the preparation conversation of [0007](0007-preparation-is-a-conversation.md)
  (the conversation is the session's own, with the user), "modes fix models and effort" of
  [0011](0011-kept-and-rejected.md), the lead's work orders of [0012](0012-the-lead-gives-work.md) as a way to
  direct a writer (`decide {work}` records the session's new diagnosis after a stall), and the 0.20 rule that
  the lead does not design. 0.20 stopped a lead that designed in the middle of a task, through requirements
  and amendments, with nobody challenging it; here the design is made once, in a named phase, saved, challenged
  and approved, and a later doubt revises that record.
- Medium has no independent reader. Its reader knows what was asked, and can be talked into the session's own
  mistake: 22 of 72 findings were behaviour defects an independent reader caught. Heavy is where that reader
  is bought; the receipts of real tasks are what will show whether medium needs one.
- A plan of many stages with no checkpoint is read only at the end. The advisor is told to challenge the
  placement; nothing refuses such a plan.
- The session can edit any file. The scope is audited, not enforced: a change outside the plan is reported
  before anything is read, and the session says whether it is its own.
- A task the session writes is not isolated: `-w`, `worktree.default` and `staging.isolate` apply to
  `--workers`.
- The session's model is the user's choice. The receipt records it; the tool cannot set it.
- A long task lives in one conversation. Its state is on disk, so `status` returns the open stage after a
  compaction, and `take` continues it in a fresh session on the user's word.
- Validated by scenario tests with scripted workers and a dry run with recorded answers; no live model ran.
  That cannot show whether a session follows what a phase asks, or whether a reading at a checkpoint finds
  what a reviewer of every stage found. The first real tasks, and the spend their receipts record, are that
  validation.
