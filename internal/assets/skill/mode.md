# The mode

A mode is the shape of the process, not a row of models: how far the questions go, what the plan must
contain, and which kind of second look the work gets, and where. `agentgate modes` prints both tables.

## When you write the task (the default)

| | light | medium | heavy | ultra |
|---|---|---|---|---|
| Questions | only those that block | full | full | full, and authorizations for proof |
| Plan must hold | stages and checks | `like` per stage, `grounding`, requirements | the same, and your design | the same, and an executed proof for what a risky stage completes |
| The plan is read by | nobody | the advisor, with the conversation | the advisor and independent critics | the same; the critics design blind first |
| Start | at once | on the user's approval | on approval | on approval |
| The code is read | by you, at the end | by the advisor | by independent lanes | by the ultra lanes |
| Where | after the last stage | at checkpoints and after the last stage | the same, and at every stage with its own risk | the same |
| Blocking | P0, P1 | P0, P1 | P0, P1, P2 | P0, P1, P2 |

Where it comes from, strongest first: `mode` in the plan; a flag in the request (`-l`, `-m`, `-h`, `-x`);
medium. The plan's `assessment` never raises it: when its facts suggest more, `plan save` says so in
`decision.because` and you tell the user. The user decides what a task may spend; the price is in the brief.

Changing it: before `start`, set `mode` in the plan and save it — a mode above light needs the plan read
(`architecture`) for that mode. During the task: `amend` with `{"update": "…", "mode": "heavy"}`; accepted
stages keep what they were accepted with.

## With `--workers`

The mode fixes the models and the effort of every role. Where it comes from, strongest first:

1. `mode` in the plan.
2. A flag in the request.
3. The plan's `assessment`: light for a trivial exact change of at most three files; heavy for a named risk
   or a complex uncertain change; ultra for an irreversible risk, or a severe one in complex work; medium
   otherwise.

- **Before preparation:** put the flag in the request.
- **After preparation, before `start`:** `prepare` with `{"mode": "heavy"}` alone: the saved plan takes it and
  no worker runs. A mode with architects needs its design pass again.
- **During the task:** `amend` with `{"update": "…", "mode": "heavy"}`. A mode with architects needs an
  accepted architecture before its writers start.

When a flag or the plan sets a mode below what the assessment gives, `because` says so with the risks: tell
the user what the lower mode leaves out.

Effort is earned, not assumed: do not raise the mode or the effort because a task feels important. A
correction that went nowhere raises its writers one step by itself when you give them the new diagnosis;
`work[].escalate` raises one writer one step; one stream that is hard by a fact you can name may carry
`effort` in the plan. Nothing goes above the mode's `escalate_to`.
