# Needs, and giving work

A need is something only a person can settle. Each carries `what` (the cause, the tool's own words first),
`resolve` (the one way to settle it), and `command` with `input`: that way as it is sent. Write the input to a
file, replace every `<…>`, and run `agentgate <command> --file <file>`. `decide` and `answer` run the task on
by themselves.

## answer — a writer stopped with questions

The other streams went on; only the streams that wait for this one are held. Decide — ask the user when the
decision is theirs — and answer into the writer's own session:

```json
{"stream": "page", "answer": "Use the giveawayPrize query; the slug is the route parameter."}
```

The streams it waits for (`after`) run first. When the answer is a different plan, `amend` instead.

## failed — a writer or its provider failed

`run` retries the unfinished streams and keeps the finished ones. A writer that wrote no event for ten minutes
was stopped (`no event for 10m0s`); `run` continues it in its own session. When every account is out of
quota, `agentgate accounts` says when one returns: `pause` with that reason.

## review — you are the reviewer

Only with a configuration that sets `review: []` for the mode. `agentgate show review` prints the changed
files, the slop signals, the criteria, the checks and the input. Read the patch, judge every added line —
does the task need it? — then `decide`:

```json
{"review": {"summary": "Read the patch of claim.ts and its test; the owner check is in place.",
            "findings": [{"id": "", "severity": "P1", "confidence": "high", "category": "logic", "path": "src/claim.ts",
                          "line": 42, "evidence": "returns before the owner check", "impact": "a foreign claim succeeds",
                          "remedy": "check the owner first", "preserve": "the response shape", "boundary_break": false}],
            "quality": {"requirements": [{"id": "R1", "status": "partial", "cause": "code", "reason": "the guard is missing",
                                          "evidence": [{"kind": "code", "ref": "src/claim.ts:40-45", "detail": "no owner comparison"}]}],
                        "rules": []}}}
```

`quality` only when the packet has one: every requirement and rule exactly once. Evidence `ref`: `path:line` or
`path:first-last` for code, in a file of the stage's scope or context; a check's id or a part of its command
only it contains (`client:test`); an observed scenario id for the browser. A requirement that is not verified
needs its `cause` (below). An empty findings list is a valid result. Every problem of a review comes back in
one answer.

## finding — a dispute the reviewer upheld, or a finding that waits for an approval

Read both arguments, then:

```json
{"findings": [{"id": "<id>", "decision": "accepted", "evidence": "src/claim.ts:42 returns before the owner check"}]}
```

`accepted` sends it to its writer; `rejected` needs contrary evidence from the code; `deferred`;
`needs-approval`. Accepting a P3 needs `basis` (the agreed criterion or the user's words). A finding without a
path needs `stream` or `files`. `duplicate_of: "<id>"` closes it as a duplicate, whatever `decision` says.

## requirement — a criterion that is due is not verified

`what` gives the reviewer's cause:

- **code** — the code does not do it. It usually went to its writer by itself; it reaches you when no single
  stream owns it, when the writer disputed it and the reviewer kept it, or when the corrections went nowhere:

  ```json
  {"work": [{"stream": "api", "requirement": "R3", "note": "Refuse a foreign owner: claim.ts has no owner check"}]}
  ```

- **proof** — the code may do it and the evidence is missing. Collect it — a declared check that exercises the
  code (`check`), a browser pass — and `run`; `run {"recheck": true}` has the reviewer judge unchanged evidence
  again.
- **plan** — the plan puts it where it cannot be met, such as a stage before the one that builds it. `amend`:

  ```json
  {"update": "R3 is completed by the page stage", "requirements": [{"id": "R3", "stage": "page"}]}
  ```

Never weaken a requirement to pass. Changing what it asks needs the user's word in `authorized`.

## rule — a blocking project rule is not satisfied

A defect: `{"work": [{"stream": "api", "requirement": "<rule id>", "note": "<what to change>"}]}`. Missing
evidence: make it available and `run`, or `pause` when it cannot be had.

## stall — a correction made no verified progress

When checks still fail, `what` names each with its verdict line — for a layout check, its
`layout measurements:` line — and `detail` holds the end of each log. Do not repeat the correction. Diagnose — `agentgate consult` can investigate — and give the writers the new diagnosis:

```json
{"work": [{"stream": "api", "note": "The failure is the cache key, not the handler: key the cache by account id."}]}
```

Its writers get one more step of effort by themselves. `pause` and ask the user when a decision is missing.

## limit — three corrections ran in a row without you

`show findings` shows what keeps coming back. `run` continues; `amend` or `pause` when the plan or a decision
is what is wrong.

## setup — something outside the code is missing

- A prerequisite of the plan does not exist yet (`id` names it, `detail.command` how it is made): a database,
  a migration applied to it, a stand. Make it exist — with the user's word where `setup.authorizations` of the
  preparation asks for it — then `{"prepared": ["<id>", …]}` to `run`. The writers of the stage, or the checks
  after the stream it is made from, wait for nothing else. While you make it exist, ending your turn in the
  first minutes is not held against you by the Stop hook.
- A check that passed on these same files fails now: the last line of its log comes first. Repair what it
  names — a stopped service, a stand, the machine — and `run`: the check runs again, no writer starts. If the
  code is at fault after all, `{"work": [{"stream": "<stream>", "note": "<what is wrong>"}]}` sends the failure
  to that writer.
- A design could not be imported, or a page could not be observed: the `design` page.

## scenarios — opted-in browser scenarios were not observed

`run {"visual": {"url": "http://localhost:4200", "origins": ["http://localhost:4200"]}}`, or
`visual {"skip_reason": "<why>"}`. Only the scenarios of this stage are due — those whose `stage` names it, the
last stage's without one, every scenario in the pass over the whole change — and a skip covers this stage
only. A pass that observed its scenarios is recorded even where the reviewer could not establish everything:
`did` and the receipt say what was not established. What a browser cannot do — slow or reordered responses, a
hidden tab, controlled time — is a test's to prove, not a scenario's.

## scope — an isolated task changed files no stream owns

`amend` gives them to a stream when the task means to change them:

```json
{"update": "the generator writes the client next to the schema", "scope": [{"stream": "api", "add": ["src/generated/client.ts"]}]}
```

Generator output a tool rewrote on its own — a service started from the worktree rewrites its schema — is put
back instead: the need's `input` is `{"restore": [paths]}` to `run`, which restores them as the position found
them and goes on.

## conflict — a review without --fix found conflict markers

Report them: a read-only review cannot change code.

## Giving work without a need

`decide {"work": [...]}` gives work to the writers of named streams at any step after the round of writers
reported, and also at `done`. New work on a finished task — a revised design, requirements the user adds — is
`amend` with named operations instead: it adds a stage (the `finished` page). An item is `{stream, note, requirement?, escalate?}`: `note` is what to do;
`requirement` names an unmet requirement or rule — the work is tracked until the criterion is met; `escalate`
raises that writer one step of effort. Failing checks go to the streams you name. A correction opens at once
with everything outstanding: accepted findings, failing checks and your work.
