# Extensions

[Русский](ru/extensions.md)

An extension is one JSON file that plugs something into `agentgate` without touching its source or your main
configuration: a code graph for the workers, a workspace index, what a worktree needs for them. Drop the file
in, and it is on; remove it (or rename it to something that does not end in `.json`), and it is off.

```
~/.config/agentgate/extensions/*.json     yours, for every repository
<repository>/.agentgate/extensions/*.json  for one repository (travels with the plans when that directory is synced)
```

```sh
agentgate config extensions    # which files are loaded and what each adds
agentgate config show          # the configuration after they are merged in
```

An extension holds ordinary configuration keys. Objects merge into the configuration key by key. Lists **add**
their items to the list that is already there, so two extensions can each bring a file for the worktree
without knowing about each other. `about` is a note for people. An extension cannot define accounts.

At each level, extensions load before its explicit config: user extensions/config, repository
extensions/config, then the personal checkout config. Explicit lists replace extension defaults.
`config extensions` also reports effective review rules and their definition sources in precedence order;
the source list is a definition history, not per-field provenance. See [configuration](configuration.md).

## Project review rules

`review_rules` adds local review instructions to the existing review pass. Writers receive the same
applicable rules; the first reviewer assesses them alongside acceptance criteria. Light mode uses the lead.
Preparation sees a scoped catalogue before discovery and reports applied rules after it. Architects, scouts
and scoped UI designers receive the same instruction snapshots. Changed applicable policies invalidate an
accepted architecture and the cached discovery as well as review.

```json
{
  "about": "Public-contract review for the application",
  "review_rules": {
    "contracts": {
      "instructions": ["docs/review-contracts.md"],
      "files": ["src/api/**", "src/shared/**"],
      "mode": "blocking"
    }
  }
}
```

- `instructions` lists nonempty local files, including a `SKILL.md` if appropriate. Relative paths start at
  the checkout root; absolute paths and `~/` work too. Declare supporting documents explicitly. Contents are
  snapshotted into assignments, so reading a skill does not load plugins or run its workflow.
- `files` selects the declared task scope with repository-relative globs (`*`, `?`, `**`); omitted means
  every task. A rule outside the scope is not loaded. Prefer versioned instruction files available in worktrees.
- `mode` is `blocking` by default or `advisory`. A mandatory rule needs `pass` or justified `not_applicable`.
  Missing mandatory instructions refuse work; unavailable advisory rules are reported and do not block.

Adding, changing or removing an applicable rule requires a fresh review. Instructions grant no new tools,
tests, servers or permissions. Rules do not add model processes: an existing lane owns their assessment.
Substantive defects still use ordinary findings and triage; an advisory rule cannot downgrade a defect found
under a built-in review axis. The JSON result and evidence formats are in `agentgate reference`.

## Repository frontend skills

Keep team instructions in Git, for example `.agents/skills/client-ui/SKILL.md`, and register their acceptance
criteria in `.agentgate.json`:

```json
{"review_rules":{"client-ui":{
  "instructions":[".agents/skills/client-ui/SKILL.md"],
  "files":["apps/client/src/**"],
  "mode":"blocking"
}}}
```

Choose the real paths of your repository. A short declarative skill can be both the implementation guide
and review criteria. For a procedural skill, point `instructions` at its acceptance reference instead and
link the skill from the relevant `AGENTS.md`. Declare every reference that defines acceptance; these files
are snapshotted, while scripts/workflows run only when the assignment authorizes them. No skill registry or
automatic installation is involved. Personal additions can use `.agentgate/config.json` and absolute or `~/`
instruction paths; versioned relative paths are easier to share and reproduce in a worktree.

The built-in design skill governs package/evidence handling. Component choice, CSS techniques, tokens,
breakpoints and control behavior come from project rules and the accepted task. A still image does not
establish interactivity. A material conflict needs a decision, not a silently imposed global preference.

## Tools for workers

Workers run without your MCP servers and plugins on purpose: a review must not depend on what one machine has
installed. `worker_mcp` hands chosen roles chosen tools all the same.

```json
{
  "about": "graphify code graph for workers",
  "worker_mcp": {
    "graphify": {
      "command": "graphify-mcp",
      "args": ["graphify-out/graph.json"],
      "tools": ["query_graph", "get_node", "get_neighbors", "shortest_path"],
      "roles": ["scout", "architect", "reviewer", "consultant"],
      "hint": "a graph of this repository's code. Ask it who calls a symbol before you grep.",
      "refresh": ["graphify", "update", "."]
    }
  },
  "worktree": {"copy": ["graphify-out"]}
}
```

| Key | Meaning |
|---|---|
| `command`, `args`, `env` | how the stdio MCP server is started; it runs inside the worker's sandbox, in the checkout |
| `tools` | the only tools a worker may call; empty means all of the server's. Enforced for both CLIs |
| `roles` | who gets it: `scout` (default), `architect`, `writer`, `reviewer`, `consultant`, `designer` |
| `hint` | when to use it; the worker is told this together with the tool names |
| `refresh` | a command run in the checkout once per operation, before the server's first use, to bring its index up to date |
| `writable` | directories of the checkout the server has to write (a cache); read-only workers get them writable |

The default role is the scout alone: it maps the code once, and what it learns reaches the writers and the
reviewers through the context pack. Heavy and ultra have no scout by default, so name `architect` for a tool
the design pass should have. Give a tool to reviewers when "who else calls this" is the question they keep asking;
give writers a tool only when they cannot work without it.

Two things to know. The sandbox is read-only for everyone but writers, so a server that builds its index on
demand needs `writable` for its cache directory, or a `refresh` that builds it beforehand. And `refresh` runs
outside the sandbox, like the repository's own scripts.

## Ready examples

[`examples/extensions/`](../examples/extensions) has two files to copy:

- **`graphify.json`** — the [graphify](https://github.com/Graphify-Labs/graphify) code graph. Install it
  (`uv tool install "graphifyy[mcp]"`), build the graph once without any model call
  (`graphify extract . --code-only`), keep `graphify-out/` out of git. The extension refreshes the graph
  before use (about two seconds once it is cached) and copies it into isolated worktrees.
- **`nx.json`** — the Nx project graph through the workspace's own `nx mcp`: projects, targets, dependencies.
  Nx writes its graph cache, so the extension opens `.nx/workspace-data` for writing.
