# Models

`agentgate modes` prints which model does what in each mode: planning, architecture, the backend and
frontend writers, the reviewer lanes, the severities that block and the effort ceiling. A route is
`<alias>:<effort>`, effort `low`, `medium`, `high`, `xhigh` or `max`.

- **The table is the user's.** `agentgate config set modes.medium.backend sonnet:high` changes it in the
  user's file; do it only on their word, never for one task. The `config` page has the layers.
- **Light mode** has one reviewer, `astra:medium`. A mode whose `review` is `[]` makes you the reviewer
  (`needs: review`).
- **Limits.** A model whose limit is exhausted is replaced by its first configured fallback (`fallbacks`); the
  answer reports it in `substitutions`, and the `receipt` at the end lists every substitution. `agentgate
  accounts` lists the CLI profiles and when an exhausted one returns. Never set `CLAUDE_CONFIG_DIR` or
  `CODEX_HOME` yourself.
- **Independence.** The model that wrote the code never reviews it: a lane whose model is an author moves to
  its first independent fallback, and a plan without an independent reviewer is refused before it starts.
- **A started stream keeps its session and its model.** To move the rest of its work to another model, close
  the stream with `done: true` and add a new stream for the rest, in the complete plan through `amend`.
- **Profiles.** `agentgate config profile codex` makes every worker a Codex model; `config profile default`
  returns to the built-in table.
- **Timeouts.** A writer is stopped after ten minutes without an event and continued in its session by the
  next `run`; its ceiling is `writer_timeout_s`, 3600 seconds by default. A reviewer's ceiling is
  `reviewer_timeout_s`. For one task: `amend {"update": "…", "timeouts": {"writer_s": 5400}}`.
