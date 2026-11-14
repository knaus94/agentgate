# Leading from Codex and the Codex-only profile

[Русский](ru/codex.md)

AgentGate can be led from Claude Code (`/dev`), Codex (`$dev`), or both. The same task state machine,
checks, reviews, terminal dashboard and worktree workflow serve either lead. Choosing a lead integration
and choosing worker routes are related setup decisions; the effective configuration determines the models
that actually run.

## Install the lead integration

```sh
agentgate setup install --lead claude
agentgate setup install --lead codex
agentgate setup install --lead both
agentgate setup doctor
```

`claude` installs `/dev`; `codex` installs `$dev`; `both` installs both integrations. An explicit single-lead
choice removes AgentGate's integration for the other lead. With no explicit choice, installation reuses the
saved selection or asks when a terminal is attached. The release install script also accepts
`AGENTGATE_LEAD=claude|codex|both`.

On a setup with no selected profile, `--lead codex` also selects the built-in `codex` worker profile. An
existing custom profile is preserved. Switching explicitly to Claude or both leaves the built-in `codex`
profile if that was active. Inspect the resulting routes with `agentgate modes`.

For Codex the installer writes:

| Artifact | Default path | Purpose |
|---|---|---|
| `dev` skill and its invocation policy | `~/.agents/skills/dev/` | Explicit `$dev <task>` invocation |
| Executable prefix rule | `~/.codex/rules/agentgate.rules` | Allow the lead to request execution of AgentGate outside Codex's own sandbox |
| Stop-hook entry | `~/.codex/hooks.json` | Ask the lead to continue an unfinished task when it tries to end its turn |

`CODEX_HOME` changes the Codex rules and hooks location. The skill directory remains under the user's home.
The installer asks you to review and trust the AgentGate Stop hook in Codex with `/hooks`. Check this against
the Codex version you run; a hook entry alone does not establish that the host has enabled it. Task commands
and the dashboard can be used independently of the hook.

## Run a task

Start a new Codex session in the repository and invoke the installed skill explicitly:

```text
$dev fix the pagination limit
$dev -h review the payment state transition
```

The lead prepares structured requests and calls AgentGate. A separate terminal can observe the same checkout:

```sh
agentgate task watch
agentgate task status --all
```

The tool runs workers through bubblewrap, writes its own state and connects to provider CLIs. A containing
sandbox may prevent these operations. When AgentGate detects confinement, its error asks the lead to write
input to a file and make a plain command invocation, for example:

```sh
agentgate task launch --file plan.json
```

The installed rule contains the `agentgate` command prefix and, when available, its absolute executable path.
Keep the invocation separate from pipelines, heredocs and shell wrappers so the host can evaluate that
executable rule. Host approval and sandbox policy still apply; the rule does not override a managed policy.
Workers retain AgentGate's own mount restrictions. Their precise read, write and network boundary is
explained in the [security and trust model](../SECURITY.md).

## The shipped Codex-only profile

The profile's routes are configuration defaults, not a model ranking or a guarantee of account access.
The source of this table is [the bundled profile](../internal/assets/profiles/codex.json), layered over
[the base configuration](../internal/assets/config.json). Personal and repository overrides can change it.

| Role | light | medium | heavy | ultra |
|---|---|---|---|---|
| Context | lead context | scout `terra:medium` | accepted architecture | accepted architecture |
| Architecture | lead | lead | `astra:high` + `sol:xhigh` | `astra:max` + `sol:max`, then cross-critique |
| Writers | `sol:medium` | `astra:medium` | `astra:high` | `astra:high` |
| Architecture and simplicity review | lead | `sol:high` | `sol:xhigh` | `sol:xhigh` |
| Correctness review | lead | `sol:high` | `sol:xhigh` | `sol:max` + `terra:max` |
| Security review | — | — | — | `sol:xhigh` |
| Escalation ceiling | `high` | `high` | `max` | `max` |

Other configured roles are `sol:medium` for consultation, `astra:medium` for UI design and `terra:high` for
the optional browser pass. These are alias-and-effort pairs. The actual provider model identifiers come
from `models` in the effective configuration.

The profile assigns substantial writing and review to different model identities. The runtime excludes
any model that authored part of the change, including failed partial writers, previous stages and fallbacks.
If no eligible reviewer remains, the task reports a routing failure rather than treating self-review as
independent evidence. Separate aliases pointing to the same model do not satisfy this rule.

All default routes in this profile use Codex. They therefore do not provide cross-provider review, and
parallel calls may share account limits. Configured [accounts](accounts.md) allow account selection and
quota failover; the dashboard and receipts report actual substitutions. Pool names do not promise separate
billing or unlimited parallel capacity.

## Inspect and adapt the profile

```sh
agentgate config profiles
agentgate config profile codex
agentgate config show
agentgate config validate
agentgate modes
```

A profile is a layer below explicit settings. For example, this changes the medium backend writer's effort
while keeping its configured model identity:

```sh
agentgate config set modes.medium.backend astra:high
agentgate config validate
agentgate modes
```

`agentgate config profile default` returns to the built-in mixed-provider table; explicit overrides remain
in place. Remove an override with `agentgate config unset <key>`. Custom profiles live under
`~/.config/agentgate/profiles/<name>.json`, or the corresponding `XDG_CONFIG_HOME` directory.

Configuration validation checks syntax and relationships. It does not query a provider to prove that an
account exposes a model, effort level or subscription feature. Verify your installed CLI and available
models, then evaluate representative repository tasks. Useful observations are completion time, failed
checks, correction rounds, review findings and whether the final change meets its requirements. This
profile makes no claims about current prices, benchmark scores or comparative model performance.

See [model routing and independence](model-selection.md), [configuration](configuration.md) and
[CLI reference](cli.md) for the remaining commands and routing rules.

## Interactive questions

The lead prefers Claude Code's `AskUserQuestion` or the native input tool exposed by Codex. The tool/client
renders the form; Agentgate does not create a separate question UI. Questions are grouped within the tool's
limits. A pending asynchronous question stays pending; a dismissal, empty response or timeout is not consent.
An unsupported form falls back to concise text. Only unresolved decisions are asked again.

Some Codex versions expose questions in Default mode through an opt-in development feature. Check support
with `codex features list`; when supported and desired, enable it with:

```sh
codex features enable default_mode_request_user_input
```

Start a new Codex session (or restart its editor agent) so it loads the setting. ACP clients such as editors
must also advertise form elicitation. Enabling the flag cannot add UI support to a client that lacks it.
Agentgate's installer does not silently change this global experimental setting. Claude's `/dev` command
includes `AskUserQuestion`; the selected lead/profile remains your choice.
