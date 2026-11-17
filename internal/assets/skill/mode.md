# The mode

The mode fixes the models and the effort of every role (`agentgate modes`). Where it comes from, strongest
first:

1. `mode` in the plan: `light`, `medium`, `heavy` or `ultra`.
2. A flag in the request: `-l`, `-m`, `-h`, `-x`.
3. The plan's `assessment`: light for a trivial exact change of at most three files; heavy for a named risk
   or a complex uncertain change; ultra for an irreversible risk, or a severe one in complex work; medium
   otherwise.

## Changing it

- **Before preparation:** put the flag in the request.
- **After preparation, before `start`:** `prepare` with the mode alone: the saved plan takes it and no worker
  runs.

  ```json
  {"mode": "heavy"}
  ```

  With `notes` beside it the worker continues and revises the plan for that mode. A mode with architects needs
  its design pass again: the architecture is bound to the mode.
- **During the task:** `amend`:

  ```json
  {"update": "The user asked for heavy after the review found a race", "mode": "heavy"}
  ```

  Accepted stages keep the mode they ran in. A mode with architects (`agentgate modes`: medium, heavy and
  ultra by default) needs an accepted architecture (`agentgate architecture`, the `inputs` page) before its
  writers start.

When a flag or the plan sets a mode below what the assessment gives, `because` says so with the risks: tell
the user what the lower mode leaves out.

## Effort

Effort is earned, not assumed: do not raise the mode or the effort because a task feels important. A
correction that went nowhere raises its writers one step by itself when you give them the new diagnosis;
`work[].escalate` raises one writer one step; one stream that is hard by a fact you can name may carry
`effort` in the plan. Nothing goes above the mode's `escalate_to`.
