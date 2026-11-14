# 0005 — `run` drives the task between decisions; the author answers findings

## Context

The lead drove every transition: `launch`, `review`, `triage`, `fix`, `review`, `finish`, and `launch`
again for the next stage. Every answer carried a `next` sentence and the guide said "follow it". When the
next step is determined by the state, the lead adds nothing by taking it, and each time it does it pays a
full model turn over its whole context ([0001](0001-what-0.14-0.15-showed.md): the lead was the largest
consumer of tokens in the measured task).

Two of those steps asked the lead for a judgement it was badly placed to make:

- **Triage.** The lead judged each reviewer finding from the reviewer's cited evidence, without reading the
  code (the guide forbade duplicating the investigation). A third model ruling between two others on quoted
  text is the weakest of the three positions.
- **The finishing summary and `architecture_evidence`.** Prose the lead wrote from the writers' reports to
  satisfy the gate, which a reviewer's architecture axis had already judged from the code.

## Decision

**`run` is the one command that moves a task.** It takes the next determined step and repeats until
something needs a person:

```
write   run the pending streams, wave by wave
verify  declared checks, then independent review, then classification
        ├─ clean            accept the stage; open the next, or the pass over the whole change, or finish
        ├─ fixable          open a correction round for the owning writers and go back to write
        └─ needs the lead   stop at `decide` with a typed list of what is needed
```

It stops for: a blocked writer; a failed writer or provider (the next `run` retries); a finding the author
and the reviewer disagree about; a due requirement or a mandatory rule that is not satisfied and has no
finding to send to a writer; a correction that made no verified progress; three automatic correction rounds
in one stage; a verification tool that could not observe the result; a file that an isolated task changed
outside its plan; the end of a stage when the user asked to confirm each one; the end of the task. `run` is
idempotent: called again with nothing decided it reports the same needs and starts no model.

One thing a second `run` does do, and it costs no model: when the task waits because a check fails for a
reason that may lie outside the code — a stand that could not be observed, a check that still fails after a
correction aimed at it — the failing checks run again. A repaired service is the one change the tool cannot
see by itself, and without this the lead would have to know that `check` exists to get past it.

A file that an isolated task changed outside its plan is reported before any reviewer is started. In 0.15 it
surfaced when the finished work was committed, after the review had been paid for, and the review then had
to run again with the file in scope.

**The author answers findings.** A finding at a severity the mode blocks on, with medium or high
confidence, or one that breaks a boundary, goes to the writer that owns the file without waiting for the
lead. The writer verifies it against the code and its brief. It fixes it, or returns a *dispute* with
evidence. The reviewer that raised the finding then sees the dispute in its own session: it withdraws the
finding or upholds it. Only an upheld dispute reaches the lead, which decides with both arguments in front
of it. A finding can therefore be closed by the reviewer's agreement or by the lead's decision, never by the
author alone.

Findings below the blocking line are notes. They are listed, need no judgement and can still be accepted by
the lead.

**Summaries are assembled, not written.** The record of an accepted stage and its hand-off to the next
stage come from the writers' own reports. `architecture_evidence` is removed; the architecture axis of the
review is the evidence.

## Why this is safe

- The gate is unchanged: a stage is accepted only with a current independent review, passing checks, every
  due requirement verified and no blocking finding open. Autopilot only removes the lead's keystrokes
  between those facts.
- The author-first order is the one Cognition reports working in production ("the coder uses user
  instructions and decisions to filter reviewer findings… key to preventing looping, disobeying the user,
  doing work that is out of scope"), and the reviewer keeps a clean context, which the same report found to
  matter more than sharing it.
- The stall rules are the old ones (unchanged content, a content cycle, a recurring finding, no confirmed
  fix, the same check failing again) and still demand a new diagnosis before another attempt. The limit of
  three automatic rounds is not a round limit; it is the point where the lead looks.

In a live run the whole exchange happened inside one command: a reviewer raised a finding that broke a
repository rule, the writer answered that the lead had explicitly decided otherwise and quoted the answer,
and the reviewer withdrew the finding in its own session. Nobody was asked to triage. In another, a writer
answered a finding that was wrong on purpose with the line of code that disproved it, in 16 seconds, and
changed nothing.

## Alternatives

- **Keep the manual steps and make them cheaper.** Their cost is the lead's context, which the tool does not
  control.
- **Automatic acceptance with no dispute path.** The author would fix what a reviewer hallucinated; the
  loop would converge on the reviewer's opinion rather than on the brief.
- **Triage by the lead only in heavy and ultra.** Two triage policies are two behaviours to explain, and the
  dispute path already brings the lead in exactly where the models disagree.
- **A background daemon that notifies the lead.** Neither client can be woken by an external process; a
  blocking command with a long wait is what both support ([0008](0008-lead-protocol.md)).

## Consequences

- A clean medium stage costs the lead one command instead of four, a correction round none instead of three.
- The lead's remaining inputs are decisions: answers, rulings on disputes, plan amendments, authorizations.
- `launch`, `continue`, `review`, `fix`, `finish` and `triage` are replaced by `start`, `run` and `decide`.
- Light mode keeps the lead as the reviewer: `run` stops at `decide` with the patch, and the lead's reading
  goes in through `decide`.
