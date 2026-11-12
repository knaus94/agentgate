# The panel

[Русский](ru/watch.md)

`claude-dev watch` is a live, read-only view of what `/dev` is doing. Run it in any terminal inside the
repository (Zed: the `claude-dev: watch` task). It never drives a task and never calls a model or a provider.

```
 claude-dev · this checkout                                        claude 47% · codex 23%  21:50:18
────────────────────────────────────────────────────────────────────────────────────────────────────
 TASKS 2
▸ ▶ medium reviewing        27s     fix the calculator                             ▶ review 13s
  ✔ light  ready to land    3m11s   add rate limiting to the export endpoint       isolated
────────────────────────────────────────────────────────────────────────────────────────────────────
 fix the calculator
 medium · round 0 · session 4f2a91c0
 stages   launch 12s › ▶ review 13s

 WORKERS
   ✔ writer  core              opus:medium @claude     13s        reported COMPLETED: fixed add and mul
   ✔ review  architecture      fable:medium @claude    13s        0 findings
 ▸ ▶ review  correctness       astra:medium @codex     14s        $ cat calc.py

 CHECKS
   ✔ python3 -B -c import calc; assert calc.add(2, 3) == 5                   passed
   PLAN  1 streams · 1 files · 1 checks

 next   Verify each finding against the code and `triage` it.
 ↑↓ move · tab switch list · enter open · a every checkout · q quit
```

## What it shows

- **Tasks** — every task of this checkout, from every Claude Code session: running ones first, then the ones
  waiting (on hold, ready to land), then recent finished ones. Mode, phase, total time, the operation in flight.
  `a` switches to every checkout on the machine.
- **Stages** — the operations the selected task went through, in order, each with its duration; the running
  one is marked `▶`.
- **Workers** — scout, architects, writers, reviewers: model, effort and account, how long the run took (and
  `×N` when the lane ran several times), and for a running worker the last thing it did.
- **Checks** — each declared check and whether its result is current.
- **Findings** — open findings by severity and how many block.
- **Limits** — in the header, the last known use of each configured subscription.

## Looking inside

Move with `↑` `↓` (or `j` `k`), switch between the task list and the rows of the task with `tab`, open a row
with `enter`, go back with `esc`.

| Row | Opens |
|---|---|
| a worker | its activity as it happens: commands with the end of their output, files read and edited, what the model said, its final report. Follows the end; scrolling up stops following, `f` resumes |
| a check | the output of its last run |
| findings | every open finding: severity, place, evidence, impact, remedy, the triage decision |
| plan | the brief, the streams with their files, the accepted design |

The activity comes from the event stream each CLI writes while it works, so it is exactly what the worker
did, not a summary. Long paths into the tool's own state are shortened to `task:/…` and `state:/…`.

`claude-dev watch --once` prints the same picture as plain text, for scripts and for output that is not a
terminal. `--all` starts with every checkout.
