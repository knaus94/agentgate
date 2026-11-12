# Changelog

[Русский](CHANGELOG.ru.md)

## 0.6.0

- **Extensions.** One JSON file per integration in `~/.config/claude-dev/extensions/` or
  `<repository>/.claude-dev/extensions/` plugs it in without editing the configuration or the source; lists in
  an extension add to the existing ones. `claude-dev config extensions` shows what is loaded.
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

- **Interactive panel.** `claude-dev watch` is now a live terminal panel: every task of the checkout (or, with
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

- `claude-dev plan link <folder>` keeps a repository's `.claude-dev` (plans, helper scripts, personal settings)
  in a synced folder and links it in place; the same command on another computer picks the data up.
- `.claude-dev/config.json`: personal per-repository settings, read after `.claude-dev.json`.
- Worktree setup commands see `CLAUDE_DEV_CHECKOUT` and `CLAUDE_DEV_WORKTREE`; the checkout's `.claude-dev` is
  linked into the worktree.

## 0.3.0

- **Isolated tasks.** `/dev -w` runs a task in its own git worktree and branch: the checkout stays clean,
  several sessions may change the same files, and the finished work comes in with `claude-dev land` on the
  user's word — `apply` (uncommitted, default), `commit`, `branch` or `drop`. When the checkout moved on it is
  merged inside the worktree: checks rerun, changed overlap is reviewed again, conflicts go back to the writers.
  `worktree.copy` and `worktree.setup` prepare a fresh worktree; `worktree.default` isolates every task.

## 0.2.0

- **Several subscriptions.** `claude-dev accounts` manages CLI profiles of Claude and Codex as accounts: live
  limits per account, calls spread by remaining quota and by what is already running, an exhausted account
  rests until its limit resets, and the model is substituted only after every account refused it. A continued
  session returns to its account. `accounts discover --save` picks up the profiles on the machine; duplicates
  of one subscription and API-key profiles are not counted.
- **Saved plans.** `claude-dev plan save|list|show` keeps a plan and a readable brief in
  `.claude-dev/plans/<name>/`; every command accepts `--plan <name>`.
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
