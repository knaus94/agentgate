# Several subscriptions

[Русский](ru/accounts.md)

`claude-dev` runs the `claude` and `codex` CLIs installed on your machine. Each CLI keeps its login in a
profile directory: `CLAUDE_CONFIG_DIR` for Claude (default `~/.claude`), `CODEX_HOME` for Codex (default
`~/.codex`). An *account* is one such directory. With several accounts of a provider, `claude-dev` spreads the
workers over them and steps around the ones that are out of quota.

Without any account configured nothing changes: every worker uses the default profile of its CLI.

## Set up

```sh
claude-dev accounts discover          # what is on this machine: ~/.claude, ~/.claude-*, ~/.codex, ~/.codex-*
claude-dev accounts discover --save   # add every profile that brings new capacity
claude-dev accounts                   # limits of the configured accounts, read live
```

```
claude
  claude         ~/.claude          max 20x      5 hours 12% · week 42% · week, fable 39%
                   lead session runs here
  claude-2       ~/.claude-2        max 5x       5 hours 0% · week 8%
codex
  codex          ~/.codex           prolite      5 hours 3% · week 22%
```

To add a second subscription, give it its own directory and log in there once, in a terminal:

```sh
claude-dev accounts add claude-2 claude ~/.claude-2
CLAUDE_CONFIG_DIR=~/.claude-2 claude auth login

claude-dev accounts add codex-2 codex ~/.codex-2
CODEX_HOME=~/.codex-2 codex login
```

`accounts remove <name>` takes an account out (the directory is left alone); `accounts reset <name>` forgets a
recorded limit when you know it is back. Accounts live in the user file (`~/.config/claude-dev/config.json`,
key `accounts`); a repository's `.claude-dev.json` cannot define them — a repository does not choose whose
subscription pays for its work.

`discover --save` skips three kinds of profile, and `accounts` says why for each:

- **not logged in** — nothing to use yet;
- **the same subscription under a second directory** (for example a lean Codex profile next to the main one) —
  it adds no capacity, so it is listed but never counted twice;
- **an API-key profile** — it bills per token. It is used only if you add it yourself with `--paid`, and then
  only after the subscriptions.

## How a call picks its account

Before work starts each account is asked how much of its limits is used (the reading is reused for two
minutes). For every call the accounts of the right provider are ranked:

1. **Room for that model.** The tightest limit that applies decides: the 5-hour window, the weekly one, and a
   per-model bucket where the provider has one (Claude counts Fable separately). An account whose Fable bucket
   is exhausted still serves Opus and Sonnet.
2. **What is already running.** Each call in flight counts against its account, so lanes started together
   spread out instead of all landing on the roomiest one.
3. **The lead's own account is spared** when another has similar room: your interactive session draws from
   it, and it should be the last to run dry.

Then, when a provider refuses a call because a limit is exhausted:

```
route fable:high
  account claude-2   → limit reached     rest until its reset, try the next account
  account claude     → answers           same model, no substitution
  (every account out) → declared alternate model: opus, then astra — recorded in `substitutions`
```

- The limits of the refusing account are read again. If one that applies is exhausted, the account rests until
  that limit resets. If they still show room, the refusal was an overload or a burst limit and the pause is two
  minutes, for that model only.
- Cooldowns are stored in `~/.local/state/claude-dev/accounts.json`, so parallel `/dev` sessions and later
  runs see them.
- **The model changes only after every account refused it.** Another account gives the same quality; another
  model does not.
- A writer or reviewer that is continued (an answer to a blocker, a correction, a recheck) goes back to the
  account that holds its session. If that account is out, the same session is continued under another account
  of the same provider; it never moves to another model.

The account a lane ran on is recorded with its route and shown in `claude-dev watch` as `@name`.

## What it touches

- Reading Claude limits is one GET to Anthropic's usage endpoint with the profile's own OAuth token. The token
  is never refreshed by `claude-dev`: the CLI owns it. A profile whose token is stale shows "limits unknown"
  and is still usable — the CLI refreshes it on the next call.
- Reading Codex limits asks the local `codex app-server` of that profile.
- Inside the sandbox the host is read-only, with one exception: the login file of the chosen profile
  (`.credentials.json` or `auth.json`). When a CLI refreshes its token mid-run the new token must reach the
  profile, otherwise the profile would be logged out.
- `claude-dev doctor` lists every account and whether it can take work.
