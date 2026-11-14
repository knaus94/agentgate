# Migration and recovery

The executable name, configuration layering, saved plans and state locations are the same in every version
described here. This page covers what changes for a person who upgrades.

## 0.15 to 0.16: a new task core

The lifecycle of a task and the protocol between the tool and its lead were rewritten
([design decisions](decisions/README.md)). Configuration, accounts, saved plans, the design tools and the
panel work as before.

**Reinstall the lead integration.** The installed `/dev` command and `$dev` skill carry the workflow guide.
Run `agentgate setup install --lead <your choice>` after replacing the binary, and start a new lead session.

**Commands.** One command now moves a task between decisions.

| 0.15 | 0.16 |
|---|---|
| `launch` | `start` opens a task from a plan and runs it |
| `continue`, `review`, `fix`, `finish` | `run`: writers, checks, review, corrections and acceptance, until a person is needed. `continue` is still accepted as another name for `run` |
| `triage` | `decide {findings}`; blocking findings go to their authors without it |
| `replan` | `amend`, at any step |
| `hold` | `pause` |
| `resume` before every command on a held task | only after the user stopped the task |
| `parse`, then `prepare` with `{"request": …}` | `prepare` with the request as it was typed; `parse` remains for scripts |
| `prepare` returning `plan_json` | `prepare` returning a typed `plan`; the same worker continues with `answers` and `notes` |

A command of the earlier protocol answers with the name of its replacement. A command that is not legal in
the task's present state answers with the task's picture, the reason and the legal `actions`, and exits
with 1.

**Plans.** Saved plans are read as they are. Three things are stricter, and the validator names every
problem at once:

- a stage outside light mode needs a declared check, or the plan states `checks_waiver`;
- scope entries are plain paths, directories or patterns — a citation such as `src/a.ts:10-20 — the handler`
  belongs in the brief;
- `covers` means "works on": a requirement is due at the last stage that covers it. A plan written for 0.15
  keeps its meaning when each requirement is covered by one stage.

A planning request no longer has to be edited to be implemented: `start --implement` is the authorization.

**Tasks saved by 0.15.** They are read, shown in the panel and continued. Nothing is converted at
installation. The first command of the task's own lead session brings it to the present format and keeps the
original beside it as `state.v1.json`.

- A held task stays paused only when it waited for a person: stopped by the user, or waiting for a decision.
  A hold that stood for a blocked or failed writer is now read from the stream itself: `answer` or `run`.
- Its declared checks and the review of the present stage run once more, because their receipts were bound
  differently. Findings, decisions, writer sessions and accepted stages are kept.
- A task that 0.15 could not finish — every stage accepted and no command legal — continues with `run`; if
  its plan lacks a check, `amend` adds one or states `checks_waiver`.
- There is no way back in place. To return to 0.15, restore `state.v1.json` over `state.json`; what was done
  since is lost.

**Configuration.** No key changed. `modes.<mode>.scout` now names the route of the preparation worker only;
there is no separate scout. The default medium mode has one review lane instead of two;
`agentgate config set modes.medium.review '[…]'` restores the earlier pair.

## 0.13 to 0.14

The release keeps the existing executable name, flat commands, configuration layering, plans and state
locations. New `task` and `setup` groups are aliases. The refactor does not silently recreate an active task
or remove existing state.

## Upgrade

Stop or finish active operations before replacing their binary. Extract the complete archive and run:

```sh
sh scripts/install-local.sh --lead both
agentgate setup doctor
agentgate config validate
agentgate modes
```

Use the same `--lead` choice as your installation. The binary-only alternative is
`sh scripts/install-local.sh --no-integrations`; then update the desired integrations explicitly.
Installation preserves unrelated settings and hooks. Back up your configuration/state using your normal
backup process before changing versions if you need an immediate rollback.

## Deliberate behavior changes

| Earlier behavior | Current behavior | What to do |
|---|---|---|
| Some `--help` calls reached action dispatch | Help is read-only and works with broken config | Use `agentgate help <command>` freely |
| Unknown flags/JSON tails could be ignored | Strict flags, arity, one JSON object and a size limit | Fix typos and remove unsupported fields |
| Invalid config could be saved before validation | Edits validate before atomic replacement | `agentgate config validate`; repair the reported key |
| Baseline failure could waive a later failing check | Every nonzero required check blocks | Fix it or explicitly revise the task's agreed verification scope; do not relabel a failure as passing |
| Frontend Astra could also be a reviewer | Default heavy/ultra frontend is Opus | Inspect `agentgate modes`; explicit personal overrides still win |
| Failed correction had no usable resume path | Saved unfinished rounds can continue | `task run` |
| Broken state could look like no task | Corruption is reported and cannot authorize a new task | Preserve the file and restore a known good state/backup; do not delete it to bypass acceptance |
| A reused PID could be considered a live worker | Process start identity participates | Old unverified process receipts are not trusted as cancellation targets |
| Worker login file mounted writable | Private snapshot with guarded refresh writeback | Resolve reported credential conflicts using your CLI login; never paste credentials into a task |
| Figma export could damage an existing package on failure | Build, validate, then replace | Re-run a failed export; the earlier package stays available |
| Incomplete design package was partly accepted | Inventory, required files and bytes are verified | Reimport with `agentgate figma pull`; valid older exports already include inventories |
| Unknown GraphQL fixtures returned empty success | Missing data is a GraphQL error | Record required cases, add an intentional generic fixture, or explicitly use `--allow-missing` |
| All variable sets could reuse the first recorded response | New recordings are stored by canonical variable digest | Old manually authored plain `<Operation>.json` remains a deliberate fallback |
| Landing could commit unrelated staged files | Commit landing refuses unrelated staged changes | Commit/unstage them yourself, or use the normal uncommitted apply mode |

Old receipts without the newer implicit check fingerprint may need checks to run again. Reusing an old
nonzero `Preexisting` receipt no longer satisfies a check. This is an intentional correction of acceptance
semantics, not a reason to manually edit the gate's state.

## Interrupted work

Run commands from the task's lead session and original checkout:

```sh
agentgate task status
agentgate task run
```

`status` says where the task is, what it waits for and which actions are legal. `run` retries the unfinished
streams of a round and skips the ones already completed. A BLOCKED worker requires `answer` with the missing
decision. A task the user stopped needs `resume` first, on the user's word. A changed task requires `amend`
with the revised plan, not a blind continuation.

Completed work and the recorded origin must be retained; do not create a replacement task merely to make an
unresolved gate disappear.

## Design transport and browser setup

Set `FIGMA_ACCESS_TOKEN` to use the REST transport, or explicitly pass `--transport browser` to retain the
browser session workflow. REST failures do not trigger a hidden browser fallback. Tokens are never stored
in exported packages. `XDG_DATA_HOME` now consistently locates embedded tools and lead-private design data.

The checker tries its Playwright-managed Chromium, then a common system Chromium location. An explicit
`AGENTGATE_BROWSER_EXECUTABLE` selects an installed Chromium executable. `PLAYWRIGHT_CORE` selects the Node
module path when the package is not installed in the normal toolkit location.

`--keep` deliberately keeps newly started stand processes alive and prints their PIDs/log path. Normal
runs clean up only their own stand and fixture subprocesses. An already running unhealthy stand is an error;
the checker will not start a second server on the same port or stop a server it does not own.

## 0.14.x to 0.15: staged amendments

0.15 allowed a reviewed stage to be amended with `replan`. That path, its restrictions and its special
handling of late-admitted files are replaced by `amend` in 0.16: see [stages](stages.md#amending-the-plan).
