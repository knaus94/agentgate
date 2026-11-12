# Configuration

[Русский](ru/configuration.md)

Three layers, later ones override earlier ones key by key:

1. the built-in table ([internal/assets/config.json](../internal/assets/config.json))
2. `~/.config/claude-dev/config.json` — yours, on this machine
3. `.claude-dev.json` in the repository — the project's

`claude-dev config show` prints the merged result. `claude-dev modes` prints it as a table.
[Why these models](model-selection.md) explains the defaults.

## Models

```json
{
  "models": {
    "opus":   {"provider": "claude", "model": "claude-opus-5-5",   "pool": "claude"},
    "sonnet": {"provider": "claude", "model": "claude-sonnet-5-5", "pool": "claude"},
    "fable":  {"provider": "claude", "model": "claude-fable-5-2",  "pool": "fable"},
    "sol":    {"provider": "codex",  "model": "gpt-6.1-sol",       "pool": "codex"},
    "astra":  {"provider": "codex",  "model": "gpt-6-astra",       "pool": "codex"}
  }
}
```

- The key is an **alias**. Modes refer to aliases, so swapping a model is one edit.
- `provider` is the CLI that runs it: `claude` or `codex`.
- `pool` names the quota it draws from. It is a label for you and for `watch`; give two models the same pool
  when they share a limit.

Point an alias at another model:

```sh
claude-dev config set models.sol.model gpt-6.1-sol
```

Add a model and use it:

```sh
claude-dev config set models.haiku '{"provider":"claude","model":"claude-haiku-5-5","pool":"claude"}'
claude-dev config set modes.light.backend haiku:medium
```

### Only Claude, or only Codex

Aliases are just names. With a single provider, point every alias at models you have:

```sh
claude-dev config set models.sol   '{"provider":"claude","model":"claude-sonnet-5-5","pool":"claude"}'
claude-dev config set models.astra '{"provider":"claude","model":"claude-opus-5-5","pool":"claude"}'
claude-dev config set reviewers_excluded '[]'
```

Reviewers must still be a different model from the backend writer, so keep at least two distinct models.

## Routes

A route is `<alias>:<effort>`, effort one of `low`, `medium`, `high`, `xhigh`, `max`:

```sh
claude-dev config set modes.medium.backend sonnet:high
claude-dev config set ui_designer opus:high
```

| Key | Used for |
|---|---|
| `modes.<mode>.scout` | the cheap route that gathers the context pack; `""` turns the stage off |
| `modes.<mode>.escalate_to` | the highest effort a stalled correction or a hard stream may reach |
| `modes.<mode>.architecture` | design routes (two for a critique round) |
| `modes.<mode>.backend`, `.frontend` | writers by stream domain |
| `modes.<mode>.review` | review lanes: `[{"axes": [...], "route": "..."}]`; axes are `correctness`, `architecture`, `security` |
| `modes.<mode>.blocking` | severities that block completion |
| `modes.<mode>.critique` | cross-critique after the designs |
| `modes.<mode>.architecture_timeout_s`, `.writer_timeout_s`, `.reviewer_timeout_s` | ceiling for one design, writer or reviewer process; a plan may override the last two |
| `ui_designer`, `visual_reviewer`, `consultant` | the UI designer, the browser reviewer, read-only consultations |

## Fallbacks

```json
{"fallbacks": {"fable": ["opus", "astra"], "opus": ["fable", "astra", "sonnet"]}}
```

When a provider reports an exhausted usage limit, the lane is retried on these aliases in order, at the same
effort. A substitute is skipped if it wrote the code under review. Every substitution is reported in the task
summary. Other failures are not retried on another model.

`reviewers_excluded` lists aliases that may never review (by default `sol`, the weakest of the five on
real-repository tasks).

## Other keys

| Key | Default | Meaning |
|---|---|---|
| `max_parallel` | 4 | model processes at once when the request has no `-N` |
| `worker_memory_mib` | 512 | free memory required per worker; cgroup limits are honoured |
| `retention_days` | 14 | finished tasks older than this are removed |
| `playwright_mcp` | `~/.local/share/claude-dev/playwright-mcp/…/cli.js` | browser tooling for optional visual verification |
| `accounts` | none | several subscriptions per provider; managed with `claude-dev accounts`, user file only — see [accounts](accounts.md) |

## Reset

```sh
claude-dev config unset modes.medium.backend   # back to the built-in value
rm ~/.config/claude-dev/config.json           # back to defaults entirely
```

An edit that would make the table invalid is rejected and nothing changes.

## Browser verification (optional)

```sh
mkdir -p ~/.local/share/claude-dev/playwright-mcp && cd ~/.local/share/claude-dev/playwright-mcp
npm init -y && npm install @playwright/mcp && npx playwright install chromium
```

The reviewer reaches only origins that resolve to loopback.
