# Design decisions

Records of the decisions behind the 0.16 core. Each record states the evidence, the decision, the
alternatives that were weighed and what the decision costs. They are written to be challenged: when a
record's evidence stops being true, the decision should be revisited rather than defended.

| # | Decision | Status |
|---|---|---|
| [0001](0001-what-0.14-0.15-showed.md) | What the 0.14 → 0.15 fixes and the run logs showed | evidence |
| [0002](0002-one-state-machine.md) | One state machine: a step, a pause overlay, one table of actions | accepted |
| [0003](0003-one-plan-shape.md) | One plan shape: every task has stages; a requirement is due at its last stage | accepted |
| [0004](0004-evidence-on-git-trees.md) | Evidence on git trees; scope as paths and patterns | accepted |
| [0005](0005-autopilot-and-author-filtered-findings.md) | `run` drives the task between decisions; the author answers findings | accepted |
| [0006](0006-amendment-is-ordinary.md) | Amending the plan is an ordinary operation with scoped invalidation | accepted |
| [0007](0007-preparation-is-a-conversation.md) | Preparation is one resumable conversation with a typed plan; no separate scout | accepted |
| [0008](0008-lead-protocol.md) | Lead protocol: compact answers, typed needs, exact waiting rules, a short guide | accepted |
| [0009](0009-review-economics.md) | Review economics: one patch, collapsed generated output, criteria reassessed apart from code | accepted |
| [0010](0010-state-format-and-migration.md) | State format 2 and the migration of saved tasks | accepted |
| [0011](0011-kept-and-rejected.md) | What was kept, and what was considered and rejected | accepted |
| [0012](0012-the-lead-gives-work.md) | 0.17: the lead gives work; a need says how it is settled; the pages are a skill | accepted |
| [0013](0013-the-session-writes.md) | 0.22: the session writes the task; a mode is the shape of the process; checkpoints place the readings | accepted |

## Method

1. Read the fixes between 0.14.0 and 0.15.0 and the code they touched.
2. Measure real runs from the tool's own receipts: the operation journal, worker event streams and the
   lead transcripts of the sessions that drove them. No task state was modified.
3. Compare with published work on agent harnesses, multi-agent failure modes and specification-driven
   workflows, and with the documented limits of the two lead clients.
4. Decide against the measurements, not against taste. Every component of a harness encodes an assumption
   about what a model cannot do alone; an assumption that the measurements do not support is removed.
