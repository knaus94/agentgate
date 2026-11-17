# The task is finished

`step` is `done`. The answer carries `summary`, `receipt` and `notes`; a process that ended without `done` is
not finished.

## Report

Report from `receipt`:

| Field | Holds |
|---|---|
| `writers`, `reviewers`, `preparation` | each stream and reviewer with the route it ran on |
| `substitutions` | every model that stood in for another, and why |
| `checks` | the declared checks of the last position and their results |
| `stages` | each stage with its mode, correction rounds, findings, notes and minutes |
| `minutes`, `notes` | the whole task's time, and how many notes it left |

Say what was not run and what was excluded. Never claim more than the receipt shows.

## Notes

`notes` are findings left unfixed, one line each. Tell the user. On their word, fix one in the same task:

```json
{"findings": [{"id": "<id>", "decision": "accepted", "evidence": "The user wants it fixed"}]}
```

to `decide`. The task opens again where it ended: the writer fixes, the reviewer verifies, the task finishes
again. A note of an earlier stage, listed as `[stage] …`, has no id here: give it as work —
`{"work": [{"stream": "<stream>", "note": "<the note>"}]}` to `decide`. A landed task is closed: what it left is
a new task.

## New work before landing

The user asks for more once the task is done — a revised design, a requirement they add. While the task is not
landed it stays in this task: `amend` with named operations adds a stage after the accepted ones, with the
streams of the last stage, whose writers continue in their own sessions.

```json
{"update": "The user: «streamers are found without a code»", "architecture": {"id": "<revised receipt>"},
 "requirements": [{"id": "R9", "text": "A streamer is found by Steam link, id or page address"}],
 "scope": [{"stream": "ui", "add": ["src/features/streamers/"]}]}
```

`run` then writes, checks and reviews that stage like any other, and the task finishes again. A complete plan
sent instead has to add the stage itself. Ask the user whether anything is left before you `land`: after it,
new work is a new task.

## Landing an isolated task

On the user's word only: `land {"how": "apply"}` (uncommitted, the default), `{"how": "commit", "message":
"<in the repository's convention>"}`, `{"how": "branch"}` or `{"how": "drop"}`. The `isolation` page has the
details.

## Dropping a task

When the user wants the work undone: `drop {"reason": "<the user's words>"}`, at any step.

- An isolated task loses its worktree and its branch; the checkout was never touched.
- A task that worked in the checkout gets its files back as they were when it started. A file that changed
  after the task's writers last left it — the user's own edit, or a writer that was interrupted — is not
  overwritten: then nothing is restored, and the answer names the files. Bring them back to what the task
  left, or keep everything and end the task with `pause {"reason": "…", "cancelled": true}`.

## Forgetting a task

When the user wants it gone from the record: `forget {"reason": "<the user's words>"}` for this session's task,
or `{"task": "<id>", "reason": "…"}` for another finished or dropped task of this checkout (`status --all`
lists them). It removes the task's state, receipts, preparation and operation records. The repository and its
saved plans are not touched. An isolated task is landed or dropped first. Never delete state directories
yourself.
