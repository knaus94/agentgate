# 0012 — The lead gives work; a need says how it is settled; the pages are a skill

## Context

Three lead sessions of 0.16 (skinhole `9158130b` and `760a038a`, polygon `7556fe3e`) and the operation
journals behind them showed where a lead's time went once `run` drove the writers
([brief](../briefs/0.17-lead-experience.md)). In the longest session, 28 hours and three tasks, the lead made
18 `amend` calls, 7 refused commands, 23 look-ups in `guide`, `reference` and `help`, 64 `status` and `show`
calls, 141 commands that parsed the tool's output with `jq`, `python` or `sed`, and 18 direct reads of state
files and design-kit sources.

The searching had two causes. Half of it was missing actions: there was no way to give a writer work
(`decide {brief}` did not reach it, `decide {requirements}` was refused without an assessment, `tell` is a
note), no way to change one part of a plan, no way to fix what a finished task left, no way to drop a task.
The other half was answers the lead could not act on directly: a need named fields without their shape,
five commands answered in five shapes, and a finished task's receipt was spread over the state.

## Decision

### Three decisions for the lead

`decide` takes `findings` (rulings), `review` (the lead's own reading where no reviewer process runs) and
`work: [{stream, note, requirement?, escalate?}]`. Work opens a correction for the named streams at once, in
their writers' own sessions, at any step after their round and at `done`. The four forms it replaces —
`requirements`, `reassessment`, `brief`, `streams` — are removed, not aliased: a lead reads the protocol in
every session and keeps no stored inputs, and an old field is answered with what replaced it.

A requirement result carries the reviewer's `cause`: `code`, `proof` or `plan`. Missing code goes to the
writer that owns it without the lead, within the same limit of automatic rounds as blocking findings, and the
work is closed when the criterion is verified, not by a vote. Missing proof and a wrong plan go to the lead.
A later review pass is shown its open notes; a finding at the same path, line and axis joins the earlier one.

A diagnosis given after a stalled correction raises its writers one step of effort by itself; `escalate`
raises one writer one more.

### Changing one thing

`amend` takes named operations — `scope`, `checks`, `requirements`, `timeouts`, `design`, `mode` — beside the
complete plan. They are applied to a copy of the present plan, which then passes the same validation and the
same rules for accepted stages and changed requirements as a complete one.

`mode` lives in the plan and in the input of `prepare`, stronger than a flag in the request. When a flag or the
plan sets a mode below what the assessment gives, `because` says so with the risks.

### Answers the lead acts on

Every command about the task answers with one object; its own data is under `result`; a refusal carries
`refused` and a failure `error`, both with the task as it is. `status` adds `running {action, since}` and
`last {action, state, error}`. Each need carries `command` and `input`: the call that settles it, with the
real ids, streams and valid references filled in and `<…>` where the lead's own words go. A refusal that
depends on a value lists the valid ones.

A running writer shows how long it runs, how long it has been silent and its last event. A writer is stopped
after ten minutes without an event and continued in its own session by the next `run`; `writer_timeout_s` is a
ceiling, 3600 seconds by default.

At `done` the answer carries `receipt` — routes per role, substitutions, check results, notes, rounds and
time per stage — and `notes`, one line each. Accepting a note reopens the task where it ended.

`drop` ends a task on the user's word and takes back what it changed: an isolated worktree is removed; files
changed in the checkout are restored to the tree the task started from, unless a file changed after the task's
writers last left it, in which case nothing is restored. `forget` removes a finished or dropped task from the
record. Neither is offered among `actions`.

### One source of pages

The lead's pages live in `internal/assets/skill/`, written by situation (a need, a writer that needs another
file, changing the mode, setting the models, a Figma task, a finished task) plus the answer of every command,
where things are and what a refusal means. `agentgate install` installs them as the skill `agentgate` for
both clients; `agentgate reference <page>` prints the same text. `guide` shrank to the loop, the table of
needs and the index of pages.

### Light mode has a reviewer

Light runs one reviewer lane, `astra:medium`. The lead reviews only when a configuration sets `review: []`.

## Alternatives rejected

- **Aliases for the old `decide` forms.** Two ways to say one thing double what a lead has to read; the
  refusal of an old field names its replacement instead.
- **Queueing a command behind a running one.** The refusal names the running action and what is possible
  meanwhile (`tell`, `stop`); `status` shows it.
- **Re-routing a started stream to another model.** A stream keeps its session; one that must change hands is
  closed with `done: true` and replaced.
- **Offering `drop` and `forget` among the actions.** Both are the user's to ask for.

## Costs

- A lead of 0.16 that sends an old `decide` form is refused once, with the replacement.
- The reviewer must give a cause for every unmet requirement; a report without one is sent back to it in its
  own session, which costs a short turn.
- Light mode now spends one reviewer call per pass.
- State gains fields (`left_tree`, `dropped`, `attachments`, work orders on rounds and corrections, stage
  records with reviewers, checks and notes); its format number does not change, and fields of 0.16 that were
  removed are ignored when a task is read.

Whether the pages and the answers reduce the searching is measured in the user's next working sessions of the
same kind, from their transcripts: fewer than five look-ups, no parsing of output, no read of state.
