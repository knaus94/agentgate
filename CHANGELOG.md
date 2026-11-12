# Changelog

[Русский](CHANGELOG.ru.md)

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
