# What commands answer

Every command about the task answers with one object. Read it as it is; do not parse it with `jq` or a script.

```json
{"task": "a8b48740", "action": "implement", "mode": "medium", "step": "decide",
 "stage": {"name": "bets", "index": 3, "of": 4, "accepted": 2},
 "did": ["writers: 1 of 1 streams completed (s3.r1)", "checks: 3 of 3 pass", "review by astra:medium: 2 findings, 1 blocking"],
 "writers": [{"stream": "api", "status": "COMPLETED", "route": "opus:medium", "summary": "…"}],
 "needs": [{"kind": "requirement", "id": "R3", "what": "R3 is partial: the code does not do it — …", "resolve": "…",
            "command": "decide", "input": {"work": [{"stream": "api", "requirement": "R3", "note": "<what is missing>"}]}}],
 "checks": {"declared": 3, "passing": 3, "failing": [], "not_run": 0},
 "findings": {"open": 1, "blocking": 0, "notes": 1},
 "actions": ["decide", "run", "amend", "check", "pause", "stop"],
 "next": "Settle `needs`: each names its `command` and the `input` to send it; `<…>` is yours to write."}
```

| Field | Meaning |
|---|---|
| `step` | `write` → `verify` → (`decide`) → the next stage … → `done`; or `cancelled` |
| `stage` | where a task of several stages is |
| `paused` | `{kind, reason}`: `user` (only `resume` lifts it, on the user's word), `decision`, `step` |
| `did` | what this call changed; a phase that ran ends with its duration |
| `writers` | the latest writer of each stream. A running one adds `running_s`, `quiet_s` (seconds since its last event) and `last_event` |
| `needs` | what only a person can settle: the `needs` page |
| `checks` | the declared checks of the present position; `failing` names them by a short label |
| `findings` | open findings, how many block, how many are notes |
| `isolated` | `{worktree, branch, landed?, ready_since?}` of an isolated task |
| `unchecked_tests` | test files the task added or changed that no declared check runs: nothing here shows they pass — `amend {checks}` adds one, or tell the user |
| `summary`, `receipt`, `notes` | at `done`: what was done; who did it on which model, the checks, substitutions, time per stage; the notes left, one line each (the `finished` page) |
| `result` | what the command has of its own |
| `refused` | `{action, because}`: the command is not legal now; nothing changed; exit 1 |
| `error` | the command could not do what it was asked; the object shows the task as it is now; exit 1 |
| `running` | only in `status`: `{action, since, running_s, now, now_s}` while another call works on the task; `now` is what it is busy with |
| `notice` | agentgate changed since this task's last command: read `agentgate guide` in full before the next command |
| `last` | only in `status`: `{action, state, error?}` of the last call that ended — read it when you are unsure whether a command ran |
| `actions` | what is legal now, the action that moves the task first |
| `next` | one sentence |

## `result` of each command

| Command | `result` |
|---|---|
| `status --full`, `show all` | everything: writers in full, findings, decisions, checks, revisions, timings, the state directory |
| `show findings` | `{open, notes, resolved}` |
| `show plan` | `{plan, revisions, name}` |
| `show stage` | `{position, stages, accepted, brief}` |
| `show checks` | `{checks: [{id, argv, kind, status, returncode, log}], failing}` |
| `show review` | what to judge in a review and the input to answer it with |
| `show architecture` | the designs and blockers of the architecture pass and the plan entry that accepts them |
| `show receipt` | the receipt, also before `done` |
| `check` | `{receipts: [{check, returncode, reused, log}], failed}`; exit 2 when one failed |
| `tell` | `{posted}` |
| `land` | `{landed, files?, commit?, branch?, commit_error?}` |
| `drop` | `{reason}` |
| `stop` | `{cancellation_requested, operation}` |

Commands before a task answer in their own shape: `prepare` (`status`, `summary`, `plan`, `brief`, `mode`,
`because`, `stages`, `checks`, `setup`, `questions`, `next`), `architecture` (what `show architecture`
prints), `route`, `modes`, `preview`, `consult`, `design`. `forget` answers `{forgot, task, removed}`. Errors
without a task go to stderr. While a preparation waits on you (`BLOCKED`), `status` carries its answer under
`preparation` and its `next`, also when an earlier task of the session is done.

## Waiting

`prepare`, `architecture`, `start`, `run`, `answer` and `decide` run model workers and take minutes; the
command prints nothing until it ends. One call runs at a time per session: "already running" means your
command was not started, not that it was queued. `status` shows what runs (`running`) and how the last call
ended (`last`).
