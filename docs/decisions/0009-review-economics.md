# 0009 — Review economics: one patch, collapsed generated output, criteria reassessed apart from code

## Context

Independent review is the component the logs justify most clearly: it found real defects and its verdicts
on requirements were right ([0001](0001-what-0.14-0.15-showed.md)). It was also the most expensive worker
role, at nearly three times the writers' input tokens. Published harnesses report the opposite proportion —
evaluation at a few percent of the build. The difference was in how the evidence was presented, not in how
much judgement was asked for.

- Each changed file produced three files (`before`, `after`, `diff`) and three paths in the packet. A
  reviewer that read all three read every change three times.
- Generator output was presented like hand-written code. In one review 590 kB of an 874 kB diff was
  migration operations and generated clients.
- The packet was one JSON document of up to 61 kB that reviewers parsed with scripts.
- Medium mode ran two full lanes on two models, each reading the whole change.
- Any amendment of the plan repeated the whole review ([0006](0006-amendment-is-ordinary.md)).

## Decision

**One patch.** A reviewer is given `change.patch`: one unified diff of the stage's reviewable changes with
context lines. The current files are in the checkout it already has read-only. Pre-change versions of
modified and deleted files are under `before/` and are read only when a question needs them. After a
correction, `delta.patch` holds what changed since the reviewer's last pass.

**Generated output is listed, not shown.** A file is treated as generated when the plan says so
(`generated: [patterns]`), when `.gitattributes` marks it `linguist-generated`, or when its path is a known
generated location (lock files, `*.gen.*`, `*.min.*`, `generated/`, `dist/`). Such a file, and any single
file with more than 1,500 changed lines, appears in the packet as a line — path, added and removed lines, why it was
collapsed — and is left out of the patch. The reviewer checks its provenance (which command produced it,
whether its source is in the patch) and may open it to investigate a concrete issue.

**A small packet.** The packet is the task in order: what was asked, what to judge, where the evidence is.
Writers' notes are truncated to their first lines with the path of the full report. It holds ids and paths,
never content.

**One lane in medium.** Medium runs one reviewer for correctness, architecture and simplicity, on a model
of a different vendor than the writer in the default profile. Heavy keeps two lanes on two models and ultra
its four: there the cost of a missed defect is what the mode was chosen for. The reviewer that judged a
change still verifies its correction in its own session, one effort step lower.

**Criteria apart from code.** A review records two bindings: the code it read and the criteria it judged.
When only the criteria or their receipts change — an amended coverage, a check that now passes — the
reviewer is asked, in its session, to reassess the listed requirements and rules. It is not asked to read
the code again.

Measured on a live task with a small change (a 6 kB patch): the reassessment took 17 seconds and one command
where a fresh review of the same stage took about 50 seconds and eight; in tokens it cost about as much
(roughly 60 k input against 90 k, with a similar uncached share), because a continued session sends its
earlier context again with every request. What a reassessment saves is the re-reading, so its advantage in
tokens grows with the size of the change and is close to nothing for a small one.

**A sceptical reviewer.** The prompt states the known bias in so many words: a model reviewing model-written
code tends to find a real issue and then talk itself out of it. A reported issue is kept unless the code
disproves it; the author's dispute is evidence to weigh, not a verdict to accept.

**A report that cannot be used is corrected, not discarded.** A reviewer's answer is validated: every axis
once, findings inside the scope with evidence and a remedy, every criterion assessed with the kind of proof
it declares. Until the first live run a report that failed any of this was thrown away with the review that
produced it, and the lead saw an error. Now the problems go back to the same reviewer in its own session, at
most twice, with the instruction to correct the report and not to review again.

That run also showed which report is not a mistake: a reviewer that finds no defect in the code and judges a
requirement unproven marks its axis as needing a fix and lists no finding. The criteria verdict explains the
status, so the report is accepted as it is; the prompt now says that an axis status describes the code and an
unmet criterion belongs in `quality`.

## Alternatives

- **Send the diff in the prompt.** It would be paid for again on every turn of the reviewer's session; a
  file is read once.
- **Two lanes in medium, one of them cheaper.** Two readers of the whole change cost twice whatever each
  costs; what the second lane adds in medium was not visible in the logs.
- **Skip review for small stages.** Small changes are light mode already, where the lead reads the patch.

## Consequences

- A reviewer of generated code relies on provenance. A hand-edited generated file is caught only if its
  generator's inputs are in the patch or a check regenerates it; the writer rules already forbid such edits.
- A medium task has one vendor's opinion instead of two. `modes.medium.review` restores the second lane.
- The 1,500-line limit is a constant. It is a property of what a reviewer can usefully read, not of a mode.
