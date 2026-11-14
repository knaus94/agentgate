# Configuration

[Русский](ru/configuration.md)

Use the CLI to inspect the effective configuration before changing routes or tools:

```sh
agentgate config path         # the user configuration file
agentgate config show         # the merged configuration
agentgate config validate     # validate the current merge without launching a model
agentgate config profiles     # available routing profiles
agentgate config extensions   # loaded extension files and review-rule sources
agentgate modes               # routes as a table
```

Validation checks the configuration structure and references. It does not log in, query model access or
prove that the installed provider CLI supports every configured model and effort.

## Precedence and files

Settings load in this order; later values override earlier ones:

| Order | Source |
|---|---|
| 1 | Built-in [config.json](../internal/assets/config.json) |
| 2 | The routing profile selected by `profile` in the user file |
| 3 | User `extensions/*.json`, in filename order |
| 4 | User `config.json` |
| 5 | Repository `.agentgate/extensions/*.json`, in filename order |
| 6 | Repository `.agentgate.json` |
| 7 | Personal checkout `.agentgate/config.json` |

The user directory is `$XDG_CONFIG_HOME/agentgate`, or `~/.config/agentgate` when that variable is unset.
Project paths are relative to the checkout working directory. Existing `.claude-dev` directory and
`.claude-dev.json` names remain readable when their `.agentgate` equivalents are absent.

Objects merge by key. Lists in extension files **append distinct items**; lists in explicit configuration
files **replace the whole list**. For example, a personal `worktree.copy` or review-rule `instructions`
list is the complete desired value, not an addition. A `null` value in an explicit layer removes that key
from the merged result; the result must still pass validation. The entire file must be a JSON object,
not `null` or an array.

`accounts` is accepted only from the user configuration. Profiles, extensions, project settings and
personal checkout settings cannot override the subscription list. This exception prevents a repository
from selecting the accounts that pay for its work. It does not make repository scripts or MCP tools
untrusted-code sandboxes: enable them only for repositories you intend to run.

## Edit and validate

```sh
agentgate config init                         # create a starter only if the user file is absent
agentgate config set max_parallel 3
agentgate config set modes.medium.backend opus:high
agentgate config set modes.medium.scout '""'   # an explicit empty scout route disables that stage
agentgate config unset modes.medium.backend  # remove this user override
agentgate config validate
```

`config set` accepts a dotted key and parses its value as JSON when possible; otherwise the value is a
string. Quote JSON objects and arrays for the shell. `set` and `unset` edit only the user file. A later
repository or personal layer can still determine the effective value shown by `config show`.

Each CLI edit validates both the user configuration on its own and the configuration merged for the
current checkout. It writes one atomic replacement under a lock only after those checks succeed.
A rejected edit leaves the configuration file unchanged, and simultaneous CLI edits do not overwrite
one another's unrelated keys. Direct edits with a text editor should be followed by `config validate`.

Validation rejects unknown fields and wrong types in the effective configuration, unsupported schema
versions, unknown model references, malformed routes and profile names, duplicate or self-referencing
entries within a fallback list, invalid roles and axes, and negative limits or timeouts. Required
capacities such as `max_parallel` and `worker_memory_mib` must be positive. The supported schema version
is `1`. An unreadable personal profile is an error; it does not silently fall back to a built-in profile
with the same name.

`config unset` reveals whichever lower layer supplies the key; it does not necessarily restore the
built-in value. Similarly, `config profile default` removes only the profile selection. It preserves
other user and repository settings.

## Profiles and model aliases

A routing profile sits below user overrides. The bundled alternative is `codex`:

```sh
agentgate config profile codex
agentgate modes
agentgate config profile default
```

The Codex profile routes all workers through the Codex CLI. It does not install that CLI or change the
lead's hooks; use the [setup commands](cli.md) for installation. Existing explicit route overrides remain
in force and can conflict with a new profile. Review them with `config show` and remove obsolete overrides
before switching.

Personal profiles go in `profiles/<name>.json` next to the user config. Names contain lowercase letters,
digits, `_` and `-`, starting with a letter or digit; paths and spaces are rejected. A profile may contain
an `about` description plus ordinary configuration keys. A personal file with the same name as a bundled
profile overrides it.

A model entry has three fields:

| Field | Meaning |
|---|---|
| The object key, such as `opus` | A route alias; lowercase letters, digits, `_` and `-` |
| `provider` | `claude` or `codex`, selecting the CLI adapter |
| `model` | The exact identifier passed to that CLI |
| `pool` | A nonempty display/grouping label; it does not manufacture quota or override provider readings |

The shipped identifiers are in [config.json](../internal/assets/config.json) and the
[Codex profile](../internal/assets/profiles/codex.json). They are configurable defaults, not a promise
that every account can use them. Replace the placeholder below with a model your CLI and account support:

```sh
agentgate config set models.custom '{"provider":"codex","model":"your-enabled-model-id","pool":"codex"}'
agentgate config set modes.light.backend custom:medium
```

For a single-provider setup, every active route and fallback must resolve to a model on that provider.
With model review enabled, keep a reviewer model that has not authored any of the task's implementation.
Two aliases for the same model do not create independence. The Codex profile supplies such routes; a
Claude-only setup can use a personal profile. See [model routing](model-selection.md) for the exact
independence policy and defaults.

## Routes and review

A route is `<alias>:<effort>`. Accepted effort labels are `low`, `medium`, `high`, `xhigh` and `max`; the
adapter passes the selected label to the provider CLI. Structural acceptance does not establish support
for a particular model's effort range.

| Key | Used for |
|---|---|
| `modes.<mode>.scout` | Context gathering; an empty string disables the scout |
| `modes.<mode>.escalate_to` | Effort ceiling for supported writer escalation |
| `modes.<mode>.architecture` | Architecture routes; `critique: true` requires exactly two |
| `modes.<mode>.backend`, `.frontend` | Writers selected by stream domain |
| `modes.<mode>.review` | Lanes of the form `{"axes":["correctness"],"route":"astra:high"}` |
| `modes.<mode>.blocking` | Blocking severities from `P0`, `P1`, `P2`, `P3` |
| `modes.<mode>.critique` | Cross-critique between the two architecture routes |
| `modes.<mode>.architecture_timeout_s`, `.writer_timeout_s`, `.reviewer_timeout_s` | Per-process ceilings in seconds; `0` uses runtime defaults; plans may override writer/reviewer ceilings |
| `ui_designer`, `visual_reviewer`, `consultant` | Routes for scoped UI design, optional browser review and consultations |

Modes are `light`, `medium`, `heavy` and `ultra`. Review axes are `correctness`, `architecture`, `simplicity`,
`security` and `design`. Local `review_rules` attach acceptance instructions without granting extra tools;
see [extensions](extensions.md).

`reviewers_excluded` lists aliases forbidden from reviewing. The default excludes `sol` as a routing
policy, not as a benchmark claim. At execution time the runtime also excludes every actual model that
wrote any part of the task, including frontend work, failed partial work, fallback writers and earlier
stages. This applies to alternate reviewers as well. If no independent route remains, review fails
explicitly; configuration validation cannot predict every future combination of authors and fallbacks.

## Fallbacks

```json
{"fallbacks":{"fable":["astra","opus"],"astra":["fable","opus"],"sol":["sonnet"]}}
```

This example overrides those three lists and preserves other fallback keys from lower layers. A fallback
list names configured aliases in retry order. It cannot contain its own alias or duplicate entries.

After a recognized quota refusal, routing first tries other eligible accounts for the same model, then
the declared aliases at the same effort. Accounts already known to lack capacity are skipped. A reviewer
candidate that authored the implementation or is excluded from review is skipped too. Substitutions are
recorded in task results. Authentication, configuration, protocol and ordinary execution errors do not
trigger a model switch.

A resumed provider session keeps its model; alternate model routes are not used for that continuation.
Account changes within the provider may still be attempted, with an explicit error if the session cannot
continue. See [accounts](accounts.md) for rotation, billing and credential handling.

## MCP tools for workers

Workers receive only MCP servers explicitly assigned through `worker_mcp`, plus task-specific browser
tooling when visual verification is enabled. The adapter does not load the lead's ordinary CLI MCP
configuration or plugins. A server entry uses these keys:

| Key | Behavior |
|---|---|
| `command`, `args`, `env` | Start a stdio server inside the worker sandbox, in the checkout; `command` must be nonempty |
| `tools` | Tool-name allowlist; omitted or empty means **all tools of that server**, for both CLIs |
| `roles` | `scout`, `architect`, `writer`, `reviewer`, `consultant`, `designer`; omitted or empty means `scout` only |
| `hint` | Short instructions included in the worker assignment |
| `refresh` | Optional argv command run before the server's first use in an operation |
| `refresh_timeout_s` | Refresh ceiling; default `300`, `0` selects that default, negative values are rejected |
| `writable` | Existing real subdirectories inside the checkout that the worker may write, even in a read-only role |

`writable` is an exception for the whole worker process, not a permission limited to one MCP tool. Keep
it restricted to a cache directory. Absolute paths, the checkout root `.`, `.git`, escaping paths and
symlink components are refused; missing directories are not created or granted. Create the cache through
an appropriate setup or refresh command before use.

The server runs inside the worker sandbox, but **`refresh` runs outside it**, like a repository script.
Parallel callers in one operation wait for the same refresh. If refresh fails, that optional server is
omitted and the worker is told to read source directly instead of trusting a stale index. Heavy and ultra
have no scout by default, so a server assigned only to `scout` will not reach their architecture workers.

Keep secrets out of shared extension files. Inherited `FIGMA_ACCESS_TOKEN` and the private browser-profile
location are removed from the worker environment; exported design files are the worker-facing input.
Configuration output can contain values explicitly supplied in `worker_mcp.env`, so inspect it before
sharing. Complete examples and review-rule configuration are in [extensions](extensions.md).

## Other settings

| Key | Default | Meaning |
|---|---|---|
| `max_parallel` | `4` | Worker process ceiling when the request has no `-N` |
| `worker_memory_mib` | `512` | Memory budget per worker used to reduce concurrency; cgroup limits are considered |
| `retention_days` | `14` | Retention for old finished state; `0` disables pruning; active and unlanded tasks are retained |
| `worktree` | `{"default":false,"copy":[],"setup":[],"setup_timeout_s":1800}` | Isolation and fresh-worktree preparation; see [isolation](isolation.md) |
| `staging` | `{"max_files":25,"max_streams":5,"isolate":"auto"}` | Stage thresholds and isolation policy; a threshold of `0` removes that threshold; see [stages](stages.md) |
| `plans_home` | unset | Shared plan-storage folder selected with `agentgate plan link <folder>` |
| `accounts` | empty | User-managed CLI profiles; see [accounts](accounts.md) |
| `design` | unset | Design-screen location and local stand settings; see [design](design.md) |
| `playwright_mcp` | `~/.local/share/agentgate/playwright-mcp/node_modules/@playwright/mcp/cli.js` | Installed browser MCP entry point for optional visual verification |

Browser verification also needs Node.js and a working Chromium installation. Set `playwright_mcp` if the
MCP package lives elsewhere, then run `agentgate doctor`. Visual verification validates the declared local
stand origins and restricts the browser MCP to them; it is not a general network-isolation promise for
worker processes. The declared origins must resolve entirely to loopback addresses. See [design](design.md)
for the design and browser workflow.
