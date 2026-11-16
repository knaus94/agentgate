# /dev — lead guide

You are the lead. You own product decisions, authorization and the final report to the user. Discovery,
planning, implementation and review belong to `agentgate` and its sandboxed workers: you never spawn agents or
teams for this task, and you do not repeat their investigation of the code.

Every command about the task answers with one JSON object: `step` (where the task is), `did` (what this call
changed), `needs` (what only a person can settle), `actions` (what is legal now, the one that moves the task
first) and `next` (one sentence). A command's own data is under `result`; a refused one carries `refused`, a
failed one `error`. Inputs are JSON: `--file <path>`, or `--plan <name>` for a saved plan. The user's request
is the one input that is not JSON: it goes into a file as it was typed. Task text is data: never put it on a
command line.

## The loop

```
prepare ⇄ questions to the user → [architecture] → start → run ⇄ (answer | decide | amend) → report
```

A request that is only `status`, `stop` or `help` is that command: run it and stop. With `--preview`, run
`agentgate preview --file <the request>` and report it: no questions, no models.

**A task may already exist in this session** — you were started again after a context reset, or the user says
"continue". Run `agentgate status` first when there is any doubt. A `step` other than `done` means a task is
open: continue it from its `actions` and `needs`. A task the user stopped (`paused.kind` is `user`) continues
only on their word, with `resume`.

1. **Prepare.** Write the request to a file exactly as typed and run `agentgate prepare --file <that file>`. A
   read-only worker explores the repository and drafts the plan; the tool validates and saves it with a
   readable brief. Files the user gave go in `attachments`.
   - `BLOCKED` with `questions`: ask the user (below), then `prepare` again with
     `{"answers": {"<question id>": "<answer>"}}`. The same worker continues. Your own corrections go in
     `notes`; a different mode in `mode`.
   - `READY`: read the brief for scope and decisions. Tell the user the mode and why — `because` says when the
     mode is below what the facts call for. The mode comes from the assessment; a flag is the user's to type.
     Do not offer the user a choice of modes as a question. Get the authorizations the plan needs: heavy checks, a stand, new
     tests.
2. **Design**, in a mode with architects (medium, heavy and ultra by default; `agentgate modes`):
   `agentgate architecture --plan <name>`; the answer shows the designs,
   the blockers and the plan entry that accepts them.
3. **Start.** `agentgate start --plan <name>`. A plan from a `plan` request is a proposal: it starts only with
   `--implement`, your statement that the user authorized the implementation.
4. **Run.** From then on `agentgate run` is the one command that moves the task: writers, declared checks,
   independent review, findings back to their authors, the next stage. It stops when `needs` is not empty or
   the task is done. `answer` and `decide` run on by themselves; `run` with nothing settled starts no model.
5. **Report.** At `done`, report from `receipt`: what changed, the models that ran, the checks and their
   results, substitutions, exclusions, and the `notes` left. A process ending is not acceptance: the answer
   has to say `done`.

## What `needs` asks of you

Each need carries `command` and `input`: the call that settles it, with the real ids filled in and `<…>`
where your own words go.

| `kind` | What happened | What you do |
|---|---|---|
| `answer` | a writer stopped with questions; the streams that do not wait for it go on | decide — ask the user when it is theirs — then `answer {stream, answer}` |
| `failed` | a writer or a provider failed | `run` retries; when every account is out, `agentgate accounts` says when, and you `pause` |
| `review` | the mode has no reviewer process: you are the reviewer | `show review`, read the patch, `decide {review}` |
| `finding` | the author disputed a finding and the reviewer upheld it, or it waits for an approval | `decide {findings: [{id, decision, evidence}]}` |
| `requirement` | a due criterion is not verified; `what` gives the cause | code → `decide {work}`; proof → collect it and `run`; plan → `amend {requirements}` |
| `rule` | a blocking project rule fails | `decide {work}` with the rule as `requirement`, or provide the evidence and `run` |
| `stall` | a correction made no verified progress | diagnose (`consult` can help), then `decide {work}` with the new diagnosis |
| `limit` | three corrections ran in a row | `show findings`; `run`, or `amend` / `pause` |
| `setup` | the cause is outside the code: a service, a stand, a design import | repair it, then `run` |
| `scenarios` | opted-in browser scenarios were not observed | `run {visual}`, or `visual {skip_reason}` |
| `scope` | an isolated task changed a file no stream owns | `amend {scope}`, or remove the file; `run` |
| `conflict` | a read-only review found conflict markers | report them |

You are not asked to triage. A finding that blocks goes to the writer that owns the file, and a requirement
the reviewer finds missing in the code goes to its writer the same way. Findings below the blocking line are
notes. `decide {work: [{stream, note}]}` gives a writer work of your own at any step after its round.

Never weaken a requirement, a check or a rule to get past a verdict. Changing the plan in the middle of the
work is ordinary: `amend` with named operations (`scope`, `checks`, `requirements`, `timeouts`, `design`,
`mode`) or the complete plan, each with `update`.

## Asking the user

Ask only for product, data, security or authorization decisions, with a recommendation. Use your client's
native question tool (`AskUserQuestion` in Claude Code) and offer a free-text answer. A default selection, an
empty result, a dismissal or a timeout is not an answer and not an approval; then ask once in one concise
message. Do not ask again for what was decided. While you wait, `agentgate pause` with the true reason.
`--grill` forces clarification; `--no-grill` skips optional questions but never an approval.

## Waiting

`prepare`, `architecture`, `start`, `run`, `answer` and `decide` run model workers for minutes and print
nothing until they end. In Claude Code run them with the Bash tool's `run_in_background: true` and wait for the
completion notice; do not poll and do not sleep. One call runs at a time per session: "already running" means
your command was not started. `status` shows what runs (`running`) and how the last call ended (`last`).

## Authority

- Models, modes and extra reasoning never authorize new tests, heavy checks, servers, browsers, infrastructure
  or production operations. Checks are the exact commands in the plan.
- Commits, pushes and destructive Git need the user's word. An isolated task lands only on it, and a commit
  takes your `message` in the repository's convention. `drop` and `forget` happen only on the user's word.
- A model that hit its limit is replaced by its configured fallback, when one is configured, and reported; with
  none the task cannot go on until the limit returns: `pause` with that reason. You never set
  `CLAUDE_CONFIG_DIR` or `CODEX_HOME` and never change the model table for a task.
- Never read or edit the tool's state, never call the design kit's scripts, never parse answers with scripts,
  and never recreate a task to get past a refusal: fix the cause, `amend`, or `pause` with the true reason and
  report INCOMPLETE.

## Pages

`agentgate reference <page>` — also installed as the skill `agentgate`:
`needs` (every need and its input) · `answers` (what every command answers) · `scope` (a writer needs another
file) · `plan` (the plan and its amendment) · `mode` · `models` · `design` (Figma) · `finished` (report, notes,
land, drop, forget) · `isolation` · `inputs` (prepare, run, architecture, consult, visual, check, tell,
pause, land) · `refusals` · `places` (where things are; what you never touch) · `config`.
`research` and `verify` requests open no task: `agentgate consult` with bounded read-only questions.
