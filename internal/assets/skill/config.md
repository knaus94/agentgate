# Configuration

Layers, later ones winning: the built-in table or the selected profile; user extensions and the user's
`~/.config/agentgate/config.json`; repository extensions and `.agentgate.json`; the personal
`.agentgate/config.json`. Extensions add list items; explicit configuration replaces lists; objects merge key
by key. `agentgate config show` prints the effective settings, `config validate` checks them, and
`config extensions` lists what was loaded and where each review rule is defined.

```json
{
  "models": {"sol": {"provider": "codex", "model": "gpt-6.1-sol", "pool": "codex"}},
  "fallbacks": {"sol": ["sonnet", "opus"]},
  "modes": {"medium": {"backend": "sonnet:high",
                       "review": [{"axes": ["correctness", "architecture", "simplicity"], "route": "astra:medium"}]}},
  "staging": {"max_files": 25, "max_streams": 5, "isolate": "auto"},
  "worktree": {"default": false, "copy": [".env.local"], "setup": [["yarn", "install", "--immutable"]]},
  "review_rules": {"backend": {"instructions": [".agents/skills/backend/SKILL.md"], "files": ["apps/api/**"], "mode": "blocking"}}
}
```

A route is `<model alias>:<effort>` with effort `low medium high xhigh max`. Providers: `claude`, `codex`.
`agentgate config set modes.medium.backend sonnet:high` edits the user file; `agentgate modes` prints the table.

Per mode: `scout` — the route that prepares plans and maps the code; `architecture` and `critique` — the design
passes; `backend`, `frontend` — the writers; `review` — the reviewer lanes, each with its axes
(`correctness`, `architecture`, `simplicity`, `security`); `blocking` — the severities that block;
`escalate_to` — the highest effort a stalled correction may reach; `*_timeout_s` — process ceilings. Light has
one reviewer lane, `opus:medium`; an empty `review` makes the lead the reviewer.

Every role runs on the model the table names, also when that model writes too: which model reviews is the
user's choice. `reviewers_excluded` is the only exclusion. `ui_designer` may be set per mode
(`modes.<mode>.ui_designer`); `visual_reviewer` and `consultant` are one each.

`review_rules` add a repository's own instructions to an existing reviewer. The reviewer assesses each rule on
the files it applies to; a `blocking` rule that is not satisfied keeps a stage open, an `advisory` one is
reported. Rules grant no tools and start no workflow. Writers and the preparation worker receive the same
instructions.
