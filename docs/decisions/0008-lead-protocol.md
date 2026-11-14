# 0008 — Lead protocol: compact answers, typed needs, exact waiting rules, a short guide

## Context

The lead is a model session with a finite context that is re-sent on every turn. In the measured task its
context averaged 90 k tokens over 247 turns ([0001](0001-what-0.14-0.15-showed.md)). Three properties of the
protocol produced that:

1. **Waiting.** A command that runs for minutes returns control to the model long before it ends, and the
   model polls. The guide's advice was vague for one client and wrong for the other.
2. **Size.** The guide and the reference together are 40 kB and were read in full before the first plan,
   then again after each context compaction. Every command answered with the complete task summary.
3. **Interpretation.** What to do next was an English sentence, and a refusal was an error string. The lead
   had to infer which command was legal.

## Decision

### Waiting

Verified against the tool descriptions each client gives its model (Codex 0.167, Claude Code 2.1):

| Client | Rule in the guide |
|---|---|
| Codex | `exec_command` yields after at most 30 s. Continue with `write_stdin` on the returned session, `chars: ""`, **`yield_time_ms: 300000`**: an empty poll may wait 300 s and returns as soon as the command ends. Never poll with a shorter wait |
| Claude Code | Run the command with `run_in_background: true` and wait for the completion notice, or in the foreground with a timeout that covers the mode's limits. Do not poll |

`agentgate` prints nothing until it finishes, so an early poll returns nothing to read. Together with
autopilot ([0005](0005-autopilot-and-author-filtered-findings.md)) this takes the measured task from 196
polls to roughly one per five minutes of worker time.

### Answers

Every command answers with the same compact object:

```json
{"task": "a8b48740", "stage": {"name": "bets", "index": 3, "of": 4}, "mode": "medium", "step": "decide",
 "did": ["wrote 1 stream", "3 checks passed", "review: 2 findings, 1 blocking", "sent 1 finding to api"],
 "needs": [{"kind": "requirement", "id": "R3", "status": "partial", "reason": "…"}],
 "actions": ["decide", "amend", "pause", "stop"],
 "next": "one sentence"}
```

- `did` is what this call changed, not the task's history.
- `needs` is a typed list; each kind has one documented way to resolve it.
- `actions` comes from the state table ([0002](0002-one-state-machine.md)); an action listed is an action
  that will be accepted.
- A refusal is the same object with `refused: {action, because}` and exit code 1.
- Details are fetched when wanted: `status --full`, `show findings|plan|stage|checks|report`.

### Guide

The guide is the loop, the decision points and the authorization rules: 9 kB where it was 22 kB, and the
17 kB reference is no longer something a lead reads before its first command. Everything a lead needs only sometimes is a topic it can ask for: `agentgate reference plan`,
`design`, `inputs`, `config`, `isolation`. A lead that never writes a plan by hand never loads the plan
schema; the preparation worker receives it instead.

### Inputs

Task text and JSON still travel in files or on stdin, never on a command line. Commands that need no
content need no file: `run`, `resume`, `status`, `stop`, `start --plan <name>`.

The user's request goes to `prepare` as it was typed. The loop first had a separate `parse` step, and the
first live lead called it on the JSON it had written for `prepare`. A step that only tells the lead what the
next command would have told it is a turn spent and a mistake to make: `prepare` now reads the request
itself, and for a request that opens no task (`status`, `--preview`, `research`) it names the command that
does.

### Measured

One small task was led by a non-interactive Codex session on both versions, with the same model
([live record](../validation/live-0.16.json)):

| | 0.15 | 0.16 |
|---|---|---|
| Commands the lead ran | 14 | 6 |
| Input tokens of the lead | 757 k | 159 k |
| Worker calls | 4 | 3 |
| Input tokens of the workers | 557 k | 323 k |
| Wall time | 4 min 16 s | 3 min 14 s |

It is one run on each version. It agrees in direction with the measurements this record started from and
does not prove their size.

## Alternatives

- **An MCP server instead of a CLI**, giving both clients typed tools without input files. Tool calls that
  run for tens of minutes are where MCP clients are weakest today (call timeouts, no background mode), and
  the CLI stays necessary for the user, the hooks and the panel. A typed facade for the short commands
  remains possible on top of the same operations.
- **A daemon that owns the loop and a lead that only receives events.** Neither client lets an external
  process start a model turn; a blocked call is the only notification both honour.
- **Progress lines while waiting.** Each line would be read on a poll and stay in the lead's context. The
  panel (`agentgate watch`) is where a person follows progress.

## Consequences

- The numbers in the waiting table are properties of two client versions. They are stated in one place in
  the guide and must be rechecked when a client changes. The live lead run was non-interactive, where a
  command returns when it has ended, so it did not exercise them.
- Scripts that parsed the full summary from every answer use `status --full`.
- The Stop hook keeps its role: a lead may not end its turn on a task that is neither paused nor running.
