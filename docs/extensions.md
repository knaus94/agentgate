# Extensions

[Русский](ru/extensions.md)

An extension is one JSON file that plugs something into `claude-dev` without touching its source or your main
configuration: a code graph for the workers, a workspace index, what a worktree needs for them. Drop the file
in, and it is on; remove it (or rename it to something that does not end in `.json`), and it is off.

```
~/.config/claude-dev/extensions/*.json     yours, for every repository
<repository>/.claude-dev/extensions/*.json  for one repository (travels with the plans when that directory is synced)
```

```sh
claude-dev config extensions    # which files are loaded and what each adds
claude-dev config show          # the configuration after they are merged in
```

An extension holds ordinary configuration keys. Objects merge into the configuration key by key. Lists **add**
their items to the list that is already there, so two extensions can each bring a file for the worktree
without knowing about each other. `about` is a note for people. An extension cannot define accounts.

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

The default role is the scout alone: it maps the code once, and what it learns reaches every other worker
through the context pack. Give a tool to reviewers when "who else calls this" is the question they keep asking;
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
