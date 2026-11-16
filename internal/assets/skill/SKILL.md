---
name: agentgate
description: Pages for the lead of an agentgate task (/dev, $dev) — what each need asks and the exact input that settles it, what every command answers, changing the plan, the mode or the models, Figma work, finishing, dropping and forgetting a task, and what a refusal means. Use while leading such a task; reading it starts nothing.
---

# agentgate — the lead's pages

An answer about a task already carries the next move: `needs[].command` and `needs[].input` are the call to
make, with the real ids filled in and `<…>` where your own words go. Open a page when that is not enough.
`agentgate reference <page>` prints the same text.

| When | Page |
|---|---|
| a need of any kind; giving work to a writer | `needs` |
| the fields of an answer: `result`, `refused`, `error`, `running`, `last`, `receipt` | `answers` |
| a writer asks for a file outside its stream | `scope` |
| the plan is wrong: a stream, a check, a requirement, a timeout, a design | `plan` |
| the mode is wrong, before or during the task | `mode` |
| which models run, fallbacks, accounts, timeouts | `models` |
| a Figma design, a stand, a layout check | `design` |
| the task is done: report, notes, landing, dropping, forgetting | `finished` |
| an isolated task (`-w`) | `isolation` |
| preparation, architecture, UI design, consultations, browser checks, `check`, `tell`, `pause`, `land` | `inputs` |
| a command was refused or failed | `refusals` |
| where things are, and what you never read or run yourself | `places` |
| configuration layers and review rules | `config` |

## Commands

Inputs are one JSON object: `--file <path>`, `--file -` (stdin), or `--plan <name>` for a saved plan. Unknown
fields are refused.

| Command | Input | Purpose |
|---|---|---|
| `parse`, `preview` | the request as typed | how a request reads; the routes it would take, starting nothing |
| `prepare` | the request as typed, or `{request, name?, mode?, context?, attachments?, notes?, answers?, fresh?}` | a read-only worker drafts or revises the plan; the same worker continues on every call |
| `plan save [name]`, `plan list`, `plan show <name>` | plan | keep a plan in `.agentgate/plans/<name>/` with a readable `brief.md` |
| `route` | plan | the mode, its reasons and the models; starts nothing |
| `architecture` | plan | the design passes of heavy and ultra |
| `start [--implement]` | plan | open a task and run it |
| `run` | `{visual?, recheck?, reviewer_timeout_s?}` or nothing | every determined step, until a person is needed |
| `answer` | `{stream, answer}` | reply to a blocked writer in its own session, then run on |
| `decide` | `{findings?, review?, work?}` | rule on findings, give your own review, give work; then run on |
| `amend` | named operations, or the complete plan, with `update` | change the plan of the active task |
| `status [--full] [--all]`, `show <part>` | — | the task now; `show findings|plan|stage|checks|review|architecture|receipt|report|all` |
| `check` | `{argv}` or `--all` | run declared checks; exit 2 when one fails |
| `tell` | `{message}` | a note on the writers' board; reaches writers that are running |
| `pause`, `resume`, `stop` | `{reason, cancelled?}`, —, — | park the task; lift a pause; end the running call |
| `land` | `{how, message?}` | bring an isolated task into the checkout, on the user's word |
| `drop` | `{reason}` | on the user's word: end the task and take back what it changed |
| `forget` | `{reason, task?}` | on the user's word: remove a finished or dropped task from the record |
| `visual`, `design`, `consult` | see `inputs` | browser verification, UI design, read-only questions |
| `watch [--all]` | — | the user's panel |

Setup: `doctor`, `install`, `update`, `modes`, `config …`, `accounts …`, `prune`, `version`.
