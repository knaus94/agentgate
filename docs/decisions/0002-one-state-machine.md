# 0002 — One state machine: a step, a pause overlay, one table of actions

## Context

Until 0.15 a task had a `phase` string with nine values (`implementing`, `correcting`, `awaiting-review`,
`reviewing`, `reviewed`, `held`, `staged`, `complete`, `cancelled`) assigned in eight places. Each command
checked the phases it knew. `held` remembered the phase it interrupted in a second field, which doubled the
states every command had to consider. Three of the defects fixed in 0.15 were states in which no command
was legal, and thirteen of the refused commands in the run logs were a lead trying a command that was
legal one phase earlier ([0001](0001-what-0.14-0.15-showed.md)).

## Decision

A task's position is one of four **steps**, named after what has to happen next:

| Step | Meaning |
|---|---|
| `write` | The current round of writers is not finished (first implementation or a correction) |
| `verify` | Every stream of the round reported; checks and review must run or be reconciled |
| `decide` | Verification produced something only the lead can settle |
| `done` | The task is complete (`cancelled` is the other terminal value) |

"Reviewed and clean" is not a step: a clean verification accepts the stage in the same operation.

A **pause** is an overlay, not a step: `{kind, reason}` on top of an unchanged position. Three kinds exist,
because exactly three things wait for a person:

- `user` — the user stopped the task. Only an explicit `resume` lifts it, and the lead never does that alone.
- `decision` — the lead is waiting for the user's decision.
- `step` — the user asked to confirm every stage.

A pause of kind `decision` or `step` is lifted by any action that moves the task (`run`, `answer`,
`decide`, `amend`): taking the action *is* the decision. A blocked or failed writer is not a pause; the
stream's own status says so and `answer` or `run` deals with it.

One table (`machine.go`) defines which actions are legal in which position. Three things are derived from
it and from nothing else:

1. the guard every command passes before it changes anything;
2. the `actions` list in every answer to the lead;
3. the tests: every answer that any scenario of the suite produces is held to it. Each action it offers
   must pass the guard, an unfinished task that is not parked must be offered an action that moves it, and
   every need must say how it is settled.

`setStep` is the only assignment of the step and refuses a transition the table does not contain.

## Alternatives

- **Keep the phases and add the missing guards.** This is what 0.14.1 and 0.15.0 did. Each fix was correct
  and each left the next combination to be found by a user.
- **Event sourcing with a reducer.** A journal of events would make every transition replayable. The task
  state is small, written atomically and already carries its history (writers, reviews, corrections, plan
  revisions); a second representation would have to be kept consistent with it for no behaviour the table
  does not already give.
- **A statechart library.** The table has four steps and ten actions. A dependency would be larger than
  the thing it describes, and the project has no third-party Go modules.

## Consequences

- A lead cannot be offered an action the guard then refuses, and cannot reach a position with no action.
- The explicit `resume` before every command on a held task is gone, except after a user stop.
- An interrupted operation no longer parks the task: its running writers are marked failed and the next
  `run` retries them. Parking is reserved for waiting on a person.
- Saved tasks need their phase translated once ([0010](0010-state-format-and-migration.md)).
