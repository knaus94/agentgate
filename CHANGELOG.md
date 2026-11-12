# Changelog

[Русский](CHANGELOG.ru.md)

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
