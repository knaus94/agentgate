# Accounts and limits

[Русский](ru/accounts.md)

An account is a local login profile for an installed `claude` or `codex` CLI. Several profiles let
`agentgate` distribute workers across subscriptions of the same provider and skip accounts whose limits
are exhausted. Account selection does not change the requested model.

| Provider | Default profile | Profile override | Login file used by account management |
|---|---|---|---|
| Claude | `~/.claude` | `CLAUDE_CONFIG_DIR` | `.credentials.json` |
| Codex | `~/.codex` | `CODEX_HOME` | `auth.json` |

If no account is configured **for a provider**, its workers use that CLI's default or environment-selected
profile. Managed account rotation is then disabled for that provider; declared model fallbacks still apply.
Configuring Claude accounts does not disable Codex's default profile, or vice versa.

## Configure profiles

```sh
agentgate accounts discover          # inspect ~/.claude, ~/.claude-*, ~/.codex, ~/.codex-*
agentgate accounts discover --save   # add usable profiles that bring distinct capacity
agentgate accounts list              # refresh and show configured account limits
agentgate accounts list --json       # structured statuses for scripts
```

Discovery and listing can contact the providers to read limits. Discovery searches the home-directory
patterns above; add profiles elsewhere explicitly. With no configured accounts, `accounts list` shows
discovered profiles and explains that workers still use their default profiles.

Give each additional subscription its own directory and sign in through its CLI:

```sh
agentgate accounts add claude-2 claude ~/.claude-2
CLAUDE_CONFIG_DIR=~/.claude-2 claude auth login

agentgate accounts add codex-2 codex ~/.codex-2
CODEX_HOME=~/.codex-2 codex login

agentgate accounts list
```

`accounts add` creates the directory if necessary and records the account; it does not perform interactive
login. Relative directory arguments are converted to absolute paths. Account management reads file-backed
credentials: a login stored only in a keychain or external helper is not a discovered subscription profile.
Use a file-backed profile supported by your CLI if you need managed rotation.

`discover --save` skips profiles that are not logged in, detected duplicates, already configured homes or
names, and API-key profiles. Duplicate detection uses the provider identity when available, with a
credential-derived identity as a fallback; it cannot establish that every independently created login
belongs to a different subscription. Detected duplicates are treated as one account for retries and
concurrent load, and share recorded cooldowns.

```sh
agentgate accounts reset codex-2      # clear cooldowns, cached limits and what was learned of them; read again
agentgate accounts remove codex-2     # remove configuration; leave the profile directory untouched
```

Reset does not replenish provider quota or bypass its limits. Accounts are stored only in the user
configuration, normally `~/.config/agentgate/config.json` or `$XDG_CONFIG_HOME/agentgate/config.json`.
Profiles, extensions and repository configuration cannot supply the `accounts` list. See
[configuration](configuration.md) for precedence and validation.

## Selection and rotation

The manager reuses a limit reading for up to two minutes and refreshes it when a reported reset passes.
An explicit account listing requests fresh readings. Failed probes are also cached briefly to avoid
repeated requests from simultaneous workers.

For a new call, selection considers:

1. **Available quota for the route.** The tightest applicable general or model-specific window determines
   the headroom. A small reserve of roughly 2% prevents assigning work to an almost exhausted account.
   A scoped model limit does not exhaust unrelated models. Expired windows stop blocking selection.
2. **Work already running.** Calls in flight in the current `agentgate` process lower an account's score,
   spreading parallel lanes across accounts. This is a scheduling preference, not a shared reservation
   system for every CLI process on the machine.
3. **The lead's profile.** When another account has similar capacity, the interactive lead's account gets
   a lower priority. This does not reserve a guaranteed amount of quota for the lead; an account
   [kept for the lead](#an-account-for-the-lead) does.

A logged-in account with unknown limits can still be selected. Unknown does not mean unlimited, and the
CLI can still reject the call because authentication, access or quota changed.

On a recognized quota refusal, the manager rereads that account's limits. A successful probe starts with
a two-minute pause for the route. A later reset of an exhausted applicable window extends the pause and
applies it to the scope of that window: a model's own limit rests that model, the general limit rests the
models without a limit of their own (see [below](#limits-a-model-has-of-its-own)). If the probe cannot
explain the refusal, the whole account rests for fifteen minutes.
Cooldowns are stored in `accounts.json` under the state directory, normally
`~/.local/state/agentgate` or `$XDG_STATE_HOME/agentgate`, and are shared across sessions. Unreadable or
corrupt account state is reported as an error; it is not silently replaced with an empty cache.

The call tries the next eligible account of the same provider first. Only when none remains does routing
consider the configured alternate model aliases. Accounts already known to be exhausted are skipped
without making another failing model call. Authentication errors, malformed output and other ordinary
failures do not authorize a different model. See [model routing](model-selection.md).

### Continuing a session

A resumed worker keeps its session identifier, model and actual lane directory, including the directory
of an earlier successful fallback. Its previous account is preferred while usable. If necessary, the
runtime can try that session with another account of the **same provider**; whether the CLI accepts the
session under that account is provider-dependent. Failure is reported explicitly. A resumed session is
never silently moved to another model.

The actual account is recorded alongside the route and shown as `@name` in [watch](watch.md).

## An account for the lead

The lead session and the workers draw on the same limits when they share a subscription, and a lead cannot be
moved to another subscription the way a worker can. To keep one subscription for the lead:

```sh
agentgate accounts add main claude ~/.claude --lead-only   # a new account
agentgate accounts reserve main                            # an existing one
agentgate accounts unreserve main
```

In the configuration it is `"lead_only": true` on the account. The reservation is hard: no worker runs on
that account, nor on another directory logged in to the same subscription, also when every other account
is exhausted — the worker then goes to its model's declared fallback, as after any exhausted limit, and the
error of a worker without one names the accounts kept for the lead. A worker that began its session on the
account continues on another one.

When every account of a provider that workers are routed to is kept for the lead, `accounts` prints a
warning under the table and `doctor` reports it: all those workers will run on fallback models.

## Limits a model has of its own

A Claude subscription reports a general weekly limit and, for some models, a weekly limit of their own —
Fable's, for example. The five-hour window is common to every model. A model's own limit never stops the
other models. Whether the general weekly limit also stops a model that has its own is not assumed; it is
learned once per subscription:

- **Not yet known.** The room for the model is the worse of the two limits. At an exhausted general limit
  with the model's own limit free, the call is tried: that call is the experiment.
- **It succeeds** and the general limit still reads 100% afterwards. The two limits are separate: from then
  on only the model's own limit counts for it. A success at 98–99% proves nothing and records nothing.
- **It is refused for a limit** that no exhausted five-hour window explains. The general limit caps the
  model too: it rests until the general limit resets, and both count from then on.

What was learned is kept with the account's limits, shared by every directory of the subscription, and
cleared by `accounts reset`. The table of `accounts` names it on the line of the account ("fable: its own
limit only (seen)", "fable: also capped by the general limit (seen)" or "fable: not yet known whether the
general limit caps it") and labels the general window "week, other models" once it is known not to cap the
model.

## API-key billing

Discovery reports detected API-key profiles but does not add them. Managed use requires an explicit
`--paid`. For example, after creating a Codex API-key profile through its CLI:

```sh
agentgate accounts add codex-api codex ~/.codex-api --paid
```

For a new call, usable subscription profiles come before permitted API-key profiles. Resuming an existing
session prefers its original account, including a paid one. The managed Codex adapter also rechecks that
an account has not changed from subscription login to API-key authentication since the quota probe.

`--paid` is permission for that managed account, not a global spending cap. When no account is configured
for a provider, the CLI's normal environment authentication remains available and may bill according to
the provider setup. Explicit managed account selection removes inherited provider keys and endpoint
overrides so they do not silently replace the selected login. Managed Claude discovery currently reads
subscription OAuth credentials, not standalone API keys from the shell.

## Credentials and refresh

Each worker gets a private copy of the selected login file in its own lane home. It uses that copy for
token refresh; `agentgate` does not add a writable mount for the source login file. The CLI updates its
copy using normal file operations, including atomic replacement. `agentgate` does not implement a
separate OAuth refresh flow.

After the process exits, including after a failed turn, `agentgate` checks for a changed credential file
and writes a valid update back under a lock. It only replaces the source when that source still matches
the original snapshot. If another login or refresh changed it meanwhile, the existing profile is
preserved, the worker's refreshed copy is retained, and the call returns an explicit conflict with the
copy's path. Concurrent refreshes therefore cannot be assumed to succeed without intervention: inspect
the profile with its CLI, sign in again if needed, and retry the operation.

The lane can contain credential copies and provider session data. Treat the task state directory like a
login profile; do not attach an entire state directory to an issue or share it as a diagnostic archive.
The sandbox controls writes and masks the lead's private browser directories; it is not a general-purpose
secret vault for every readable file on the host.

## When limits are unknown

Claude limit probing reads the profile's OAuth token and makes a usage request; it does not refresh that
token. A stale token is reported as `limits unknown`, and the CLI may refresh it when the profile is used.
Codex limit probing starts that profile's local `codex app-server` and asks for account limits. Neither
probe proves that a requested model is enabled for the account.

Use `agentgate doctor` for installation and configured-account diagnostics, and `agentgate accounts list`
for the detailed windows and probe notes. Provider CLI versions and response formats can change; a
successful local test suite is not a live authentication or model-access check.
