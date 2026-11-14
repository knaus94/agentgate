# Migration and recovery — 0.13 to 0.14

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
| Failed correction had no usable resume path | Saved unfinished rounds can continue | `task resume`, then `task continue` |
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
agentgate task resume
agentgate task continue
```

Inspect the hold reason first. `resume` acknowledges the hold; `continue` is for an unfinished writer round.
It skips streams already completed in that round. A BLOCKED worker requires `answer` with the missing
decision. A task awaiting review should receive `review`, and staged work follows the next action reported
by status. A changed task requires an explicit plan update, not a blind continuation.

The old `launch` flow remains available for a compatible saved plan. Completed work and original snapshots
must be retained; do not create a replacement task merely to make an unresolved gate disappear.

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
