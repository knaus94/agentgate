# Changelog

[Русский](CHANGELOG.ru.md)

## 0.9.3

- **A stream can implement a Figma design, and the gate can tell whether it did.** `design [{package, url,
  root, stream, map}]` in a plan hands the design brief to that stream alone, measures the page before the
  work, declares the layout check and blocks only what the task made worse. A design pulled again makes earlier
  reviews stale. See `docs/design.md`.
- **`agentgate figma`** signs in once in a browser window and pulls a node as a compact package: a brief of
  about two thousand tokens for a page section, a picture and the icons. View access is enough.
- **`agentgate layout`** compares a page with a package without a model: position, size, radius, font, colour
  and what is absent, at most twenty lines. **`agentgate fixtures`** shows a screen in a recorded state with
  nothing behind it.
- **A `design` review axis.** The first reviewer is shown the design next to the page and looks for what
  numbers do not carry, and reads the file of exceptions for excuses.

## 0.9.2

- **Architects read the code themselves.** A design pass no longer waits for a scout or receives its map: in a
  measured pass the architects opened the same files with the map and without it, and the map only cost the
  wait. The stage-by-stage scouting of 0.9.1 goes with it.
- **Heavy and ultra have no scout by default.** There the design the lead accepted is what writers and
  reviewers start from. Medium keeps its scout. `agentgate config set modes.heavy.scout sol:medium` brings one
  back for the writers and the reviewers of a mode.
- **A critique and a revision continue the architect's session.** The critic in ultra and the reviser used to
  start as new processes and read the code again. They now continue the session that designed; when it cannot
  be continued, a fresh architect is started as before.

- **A private directory no worker sees.** A worker reads the host; `~/.local/share/agentgate/private` is now
  covered in its sandbox, so a session kept there for the lead (a signed-in browser profile) stays the lead's.

## 0.9.1

- **A staged plan is scouted stage by stage.** The architecture pass of a staged plan now runs one scout per
  stage, in parallel, and joins their maps into one context pack. One scout did not get through a large
  specification in its ten minutes and left the architects without a map. The first stage then finds its own
  map already made. A stage whose scout fails is named in `scout_note` and in the pack's unknowns; the rest is
  still used. Plans without stages are scouted as before.

## 0.9.0

- **Stages.** A large specification is split into stages, each written, checked, reviewed and accepted before
  the next starts. The plan lists requirements and says which stage covers each; one that no stage covers is
  refused. Every stage has its own mode, fresh workers and a review of its own change; accepted stages are
  archived and, in an isolated task, committed one by one. The whole change gets a last review for the seams
  (heavy, ultra, or three stages and more). `--step` waits for the user after each stage; the stages ahead can
  be revised with a reason.
- A single-pass plan past `staging.max_files` / `staging.max_streams` (25 files, 5 streams) is refused with the
  advice to stage it; `--single` allows it, `--staged` demands stages.
- The panel lists the stages; the timeline of operations is now labelled "steps".

## 0.8.1

- **Fable reviews in heavy and ultra.** The architecture lane of both and the first correctness lane of ultra
  run on `fable` (`high`, `xhigh`) instead of `opus`. Opus writes the back-end there, so these lanes were being
  substituted on every run; Fable writes nowhere and is never the author.
- **Fallbacks spare the Claude subscription.** Fable and Codex are the pools that run out, so each now falls
  back to the other's model of the same tier first: `fable → astra → opus`, `astra → fable → opus`,
  `sol → sonnet`.

## 0.8.0

- **Writers moved off Fable.** In heavy and ultra the backend writer is `opus` (`high`, `xhigh`) and the
  frontend writer is `astra` (same levels); Fable keeps the architects. Writing is the largest token spend of a
  run, and Fable is the most expensive model. Because the author's model never reviews its own code, the Opus
  review lanes of heavy and ultra now fall back to `fable` and report a substitution. Override per mode with
  `agentgate config set modes.heavy.backend <route>`.

## 0.7.1

- A worker now runs in a lane whose recorded path holds a symbolic link — the case of a task started before
  the state directory moved to its new name. The sandbox mounts the real path.

## 0.7.0

- **Renamed to `agentgate`** (was `claude-dev`): the tool is no longer tied to one CLI. An existing install
  updates itself with `claude-dev update` and gets the new name next to the old one. State and settings move to
  `~/.local/state/agentgate` and `~/.config/agentgate` with a link left at the old place; a repository's
  `.claude-dev/` and `.claude-dev.json` are still read. Environment variables are now `AGENTGATE_*`.
- **Codex can lead.** `agentgate install --lead claude|codex|both` installs the lead command into Claude Code
  (`/dev`), into Codex (the `$dev` skill, a rule that lets Codex run the tool outside its own sandbox, and a
  Stop hook), or into both. The first install asks.
- **Codex only.** `--lead codex` selects the built-in `codex` profile, in which every worker is a Codex model:
  Astra creates, Sol verifies, Terra reads. Chosen from public data; see `docs/codex.md`.
- **Profiles.** A profile is a complete mode table laid under your settings: `agentgate config profile <name>`,
  `config profiles`, and your own in `~/.config/agentgate/profiles/`. `null` in a layer removes a key.
- A lead that calls the tool from inside its own sandbox is told what to do instead of failing later.
- Installing for one lead no longer touches the other's install unless the lead was chosen explicitly.

## 0.6.0

- **Extensions.** One JSON file per integration in `~/.config/agentgate/extensions/` or
  `<repository>/.agentgate/extensions/` plugs it in without editing the configuration or the source; lists in
  an extension add to the existing ones. `agentgate config extensions` shows what is loaded.
- **Tools for workers.** `worker_mcp` hands MCP servers to workers by role (scout by default), limited to the
  listed tools on both CLIs, with an optional `refresh` command for the server's index and `writable` cache
  directories. Ready examples for the graphify code graph and the Nx project graph are in `examples/extensions/`.
- Claude workers can now use MCP servers, not only Codex ones.

## 0.5.0

- **Filtering model-written extras.** Writers get explicit rules, with the reason, for what a change must not
  carry. Before reviewers run, the added lines are scanned for literal leftovers at no model cost and the hits
  are returned as `slop_signals`. Reviews gain a `simplicity` axis, judged in the design lane: unrequested
  scope, structure with one user, defensive code away from a boundary, narrating comments, leftovers, results
  that are not real — each proved from the code, with a deletion as the remedy. The panel lists the signals.
- Fixed: landing an isolated task broke on file names with spaces.
- Removed code that only tests used, and a duplicated helper.

## 0.4.0

- **Interactive panel.** `agentgate watch` is now a live terminal panel: every task of the checkout (or, with
  `a` / `--all`, of the machine), the stages of the selected task with their timings, its workers with model,
  account and duration, checks, findings and the next step. `enter` opens a worker's activity as it happens —
  commands with their output, files read and edited, the model's words and its report — or a check's output,
  the findings, the plan. `--once` prints the same picture as text.
- Claude workers now write a stream of events while they run, like Codex workers, so their activity is visible
  live. Every run of a lane is recorded with its start and end.
- A running worker's receipt now carries its process id; the panel no longer shows running workers as stopped.
- A run is treated as refused for a limit only when the CLI's failure says so, not when an ordinary event
  mentions rate limits.

## 0.3.1

- `agentgate plan link <folder>` keeps a repository's `.agentgate` (plans, helper scripts, personal settings)
  in a synced folder and links it in place; the same command on another computer picks the data up.
- `.agentgate/config.json`: personal per-repository settings, read after `.agentgate.json`.
- Worktree setup commands see `AGENTGATE_CHECKOUT` and `AGENTGATE_WORKTREE`; the checkout's `.agentgate` is
  linked into the worktree.

## 0.3.0

- **Isolated tasks.** `/dev -w` runs a task in its own git worktree and branch: the checkout stays clean,
  several sessions may change the same files, and the finished work comes in with `agentgate land` on the
  user's word — `apply` (uncommitted, default), `commit`, `branch` or `drop`. When the checkout moved on it is
  merged inside the worktree: checks rerun, changed overlap is reviewed again, conflicts go back to the writers.
  `worktree.copy` and `worktree.setup` prepare a fresh worktree; `worktree.default` isolates every task.

## 0.2.0

- **Several subscriptions.** `agentgate accounts` manages CLI profiles of Claude and Codex as accounts: live
  limits per account, calls spread by remaining quota and by what is already running, an exhausted account
  rests until its limit resets, and the model is substituted only after every account refused it. A continued
  session returns to its account. `accounts discover --save` picks up the profiles on the machine; duplicates
  of one subscription and API-key profiles are not counted.
- **Saved plans.** `agentgate plan save|list|show` keeps a plan and a readable brief in
  `.agentgate/plans/<name>/`; every command accepts `--plan <name>`.
- Design receipts and context packs belong to the checkout, so a plan designed in one session can be launched
  from another.
- A token refreshed by a CLI inside the sandbox now reaches the profile it belongs to.
- `watch` shows the account a lane ran on; `doctor` lists the accounts.

## 0.1.0

First public version.

- `/dev` command for Claude Code with four modes (light, medium, heavy, ultra), chosen from the lead's
  assessment or by flag.
- Sandboxed Claude and Codex workers; configurable models, pools and fallbacks.
- Parallel independent design passes; cross-critique in ultra.
- Writers in waves with handoffs and a shared board; a blocked writer is answered in its own session.
- Review lanes on different models; corrections and rechecks continue existing sessions.
- Content-bound reviews and check receipts; baseline for checks that were already failing.
- `watch`, `doctor`, `install` (Claude Code and Zed), `update`, `config`.
