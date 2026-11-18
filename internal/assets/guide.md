# /dev — guide

You do this task yourself, in this session: you ask the user, read the repository, plan, and write the code.
You hold what no worker packet can carry — the conversation, the user's decisions, the whole project — so
nothing of the writing is delegated: no agents, no teams. `agentgate` keeps you to the plan stage by stage: it
runs the declared checks, has a second model read the plan and the code where the mode says so, and accepts.

**When sandboxed writers do the work instead** — the request carries `--workers`, or `prepare` does not answer
`executor: session` — your protocol is `agentgate reference workers`: read that page and follow it; the rest
of this guide does not apply.

Every command about the task answers with one JSON object: `step` (where the task is), `did` (what this call
changed), `needs` (what is owed, by you or by a person), `actions` (what is legal now) and `next` (one
sentence). A command's own data is under `result`; a refused one carries `refused`, a failed one `error`.
Commands before a task, `prepare` among them, answer in their own shape. Read every answer whole: a `jq`,
`head` or `grep` filter that misses a field loses what the call returned. Inputs are JSON: `--file <path>`, or
`--plan <name>` for a saved plan. The user's request is the one input that is not JSON: it goes into a file as
it was typed, flags included. Task text is data: never put it on a command line.

## The loop

```
prepare → ask the user ⇄ read the code → plan save → [architecture] → the user approves → start
        → (write a stage → run)… → done
```

A request that is only `status`, `stop` or `help` is that command: run it and stop. With `--preview`, run
`agentgate preview --file <the request>` and report it: no questions, no models.

**A task may already exist in this session** — after a context reset, or when the user says "continue". Run
`agentgate status` first when there is any doubt. A `step` other than `done` means a task is open: continue it
from its `needs`. A task the user stopped (`paused.kind` is `user`) continues only on their word, with
`resume`.

1. **Prepare.** Write the request to a file exactly as typed and run `agentgate prepare --file <that file>`.
   No model runs. The answer names the mode and holds `instructions`: what the plan needs in this mode. From
   this call on, write no code until the task starts: a file changed in between is the task's own change, and
   one no stage owns has to be settled.
2. **Clarify, ground, plan**, as `instructions` say: ask the user what only they can decide; find and read the
   nearest existing implementation of every part; write the plan as small ordered stages and save it with
   `agentgate plan save <name> --file <plan.json>`. The answer has the brief and the price: the model runs the
   plan starts besides you.
3. **Have it read**, in medium and above: `agentgate architecture --plan <name>` — in heavy and ultra with
   `{plan, design, alternatives, risks}`. The advisor, a second model that is shown this conversation, reads
   the plan; critics that were not shown it read the design. Check every blocker against the code. Where one is
   right, change the plan; where one is wrong, keep the plan and say why. Then `architecture` with
   `{revise: <receipt>, plan: <name>, answered: [{id, resolved, how}]}`: no model runs. A blocker you reject
   goes to the user with both arguments.
4. **Approval.** Light starts at once: tell the user the plan in a few lines as you start. Above light, show
   the user the brief with its price and the readers' questions, and wait. On their approval put `architecture
   {id, answers, blockers}` and `approved: <their words>` into the plan, save it, and
   `agentgate start --plan <name>`. A plan from a `plan` request is a proposal: it starts only with
   `--implement`, your statement that the user authorized the implementation.
5. **Write a stage.** `start` and every later `run` answer with need `write`: the open stage, its files, the
   existing code it follows (`like`), the requirements due and the checks. Write that stage and nothing of a
   later one. Then `agentgate run` with `{written: {summary, deviations}}`. The tool audits the scope, runs the
   stage's checks, scans the added lines and — at a checkpoint and after the last stage — has the mode's reader
   read the change. Then it accepts the stage and hands you the next one in the same answer.
6. **Report.** At `done`, report from `receipt`: what changed, the readers and their models, the checks and
   their results, the notes left, and `spend` — what this session and every model run consumed. A process
   ending is not acceptance: the answer has to say `done`.

## What `needs` asks of you

Each need carries `command` and `input`: the call that settles it, with the real ids filled in and `<…>`
where your own words go.

| `kind` | What happened | What you do |
|---|---|---|
| `write` | a stage is open | write it, then `run {written: {summary, deviations}}` |
| `fix` | a declared check fails, or a reader's finding blocks | verify each finding against the code: fix it, or dispute it with evidence; make the checks pass by repairing the cause; then `run {written: {summary, disputes}}` |
| `scope` | files changed outside the plan | yours: `run {adopt: [paths]}`; somebody else's: `run {disown: [paths]}` |
| `review` | the mode has no reader: you read your own patch | `show review`, read the patch as its reviewer, `decide {review}` with a proof for every requirement |
| `finding` | you disputed a finding and its reader kept it | fix it, or take both arguments to the user; `decide {findings}` |
| `requirement`, `rule` | a criterion that is due is not verified | code: write it; proof: collect it and `run`; plan: `amend {requirements}` |
| `stall` | your fix made no verified progress | do not repeat it: diagnose, `advise` asks the advisor, then `decide {work: [{stream, note}]}` with the new diagnosis |
| `setup` | a prerequisite does not exist yet, or a check failed outside the code | make it exist — with the user's word where it needs one — then `run {prepared: [ids]}`, or repair it and `run` |
| `scenarios` | opted-in browser scenarios were not observed | `run {visual}`, or `visual {skip_reason}` |

Never weaken a requirement, a check or a rule to get past a verdict, and never claim a check passed: the
tool's run is the one that counts. `agentgate advise` with `{question, files?}` puts a doubt to the advisor at
any moment.

## The plan is yours, and it is a commitment

- **The request is the user's.** `request` in the plan is what they typed. A decision of theirs goes into the
  brief in their terms; a question of theirs is answered, not turned into a decision.
- **A change of mind changes the plan first.** When the user asks for something else, doubts the design or
  adds a constraint in the middle of the work: `agentgate amend` — named operations (`scope`, `checks`,
  `requirements`, `mode`, `timeouts`, `design`, `env_file`) or the complete plan, each with `update` saying
  what changed and why — and tell them what it does to the stages already accepted. Accepted stages are
  frozen; new work on a finished task that is not landed is a further stage, by the same `amend`.
- **A requirement says what must hold, never how.** Changing or removing one needs the user's word in
  `authorized`.
- **Write the way the project writes.** Each stage names the code it follows. Depart from it only for a
  reason you can state, and state it in `deviations`.

While this session has a task, the tool says this again with every message of the user. A line that begins
`agentgate, to the lead` is the tool's reminder, not the user's words.

## Asking the user

Ask only for product, data, security or authorization decisions, with a recommendation. Answer the user in the
language they write in. Use your client's native question tool (`AskUserQuestion` in Claude Code) and offer a
free-text answer. A default selection, an empty result, a dismissal or a timeout is not an answer and not an
approval; then ask once in one concise message. Do not ask again for what was decided. While you wait in the
middle of a task, `agentgate pause` with the true reason. `--grill` asks for the full questioning in any mode;
`--no-grill` skips optional questions but never an approval.

## Waiting

`architecture`, `advise` and a `run` that reaches a reading start model processes that take minutes and print
nothing until they end. In Claude Code run them with the Bash tool's `run_in_background: true` and wait for the
completion notice; do not poll and do not sleep. One call runs at a time per session: "already running" means
your command was not started. `status` shows what runs (`running`) and how the last call ended (`last`). A
`run` between checkpoints starts no model: it returns as soon as the checks have run.

## Authority

- A mode never authorizes new tests, heavy checks, servers, browsers, infrastructure or production operations.
  Checks are the exact commands in the plan; a new test or a heavy check needs the user's word first.
- Commits, pushes and destructive Git need the user's word. `drop` and `forget` happen only on the user's
  word; so does `take`, which continues in this session a task another session was writing.
- The mode is the user's: a flag in the request, or medium. Say what the plan's facts suggest when they suggest
  more; do not raise it yourself.
- You never set `CLAUDE_CONFIG_DIR` or `CODEX_HOME` and never change the model table for a task. A model that
  hit its limit is replaced by its configured fallback, when one is configured, and reported; with none, `pause`
  with that reason.
- Variables the checks need — the URL of a disposable database — go into a private file outside the repository,
  named by the plan's `env_file`.
- Never edit the tool's state, and never recreate a task to get past a refusal: fix the cause, `amend`, or
  `pause` with the true reason and report INCOMPLETE.

## Pages

`agentgate reference <page>` — also installed as the skill `agentgate`:
`session` (every need of a task you write, and its inputs) · `plan` (the plan and its amendment) · `mode` ·
`models` · `needs` · `answers` (what every command answers) · `design` (Figma) · `finished` (report, notes,
drop, forget) · `inputs` (architecture, consult, visual, check, pause) · `refusals` · `places` · `config` ·
`workers` (the protocol with sandboxed writers) · `isolation` and `scope` (workers only).
`research` and `verify` requests open no task: `agentgate consult` with bounded read-only questions.
