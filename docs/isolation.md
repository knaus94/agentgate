# Isolated tasks

[Русский](ru/isolation.md)

Isolation is for a task of sandboxed writers (`--workers`). A task the lead's own session writes — the
default — always works in your checkout, and `-w` without `--workers` is refused.

By default the writers of a task edit your checkout: you see the change grow, and two sessions may not own the
same file. With `-w` a task works somewhere else, and your checkout changes only when you say so.

```
/dev --workers -w add rate limiting to the export endpoint
/dev --workers -w -h move invoice totals to integer cents      # another Claude Code session, same repository
```

## What happens

1. **Start.** The tool creates a branch `dev/<task>-<id>` from your current `HEAD` and a git worktree for it
   under `~/.local/state/agentgate/…/worktrees/`. Your uncommitted edits are not part of it.
2. **Work.** Writers, declared checks and reviewers all run inside that worktree. For the sandboxed workers
   your checkout is read-only. Task source edits appear in the worktree; any preexisting uncommitted edits
   in your checkout remain there. Setup scripts and explicitly shared directories have their own permissions.
3. **Several tasks.** Every session has its own worktree and branch, so tasks may change the same files.
   `agentgate watch` and `agentgate status` in the repository show them all.
4. **Finish.** The task passes the usual gate — checks, review, corrections — and is committed on its own
   branch; each accepted stage of a staged plan is a commit. Nobody else writes in the worktree, and what it
   holds is what lands, so a file the task changed outside its plan stops the task before anything is
   reviewed: the plan is amended to give the file to a stream, or the file is removed. The lead reports what
   was done and asks you how to bring it in.
5. **Land**, on your word:

   | `land` | Result |
   |---|---|
   | `apply` (default) | the change appears in your checkout **uncommitted**; you read it and commit it yourself |
   | `commit` | the same, committed on your current branch with a message the lead writes in your repository's convention (your hooks run) |
   | `branch` | your checkout is not touched; the branch `dev/…` is kept for a pull request |
   | `drop` | the work is thrown away |

   Afterwards the worktree is removed, and the branch too unless you chose `branch`.

## When your checkout moved on

Other tasks landed, or you committed something, while the task was working. Before landing, your current `HEAD`
is merged into the task **inside its worktree**:

- **Clean merge, other files.** Every declared check runs again on the merged code. Green: the task lands.
- **Clean merge, but the new commits changed files this task also changes.** The merged result is not what was
  reviewed, so the task reopens for a review of the delta, then lands.
- **A check fails after the merge.** The task reopens; the failure goes to the writers in their own sessions.
- **Conflict.** The markers stay in the worktree. The task reopens, the lead says how the two changes combine,
  and the writers that own those files resolve them; then checks and review as usual.

Your checkout never receives conflict markers. Landing also refuses — and changes nothing — when you have your
own uncommitted edits in the files the task lands; commit or stash them, or land as `branch`.

`land {how: "commit"}` also refuses when the checkout has any staged changes, including unrelated files, so
it cannot include someone else's index entries in the task commit. Commit or unstage them first, or use
`apply`, which preserves the index. New edits made to a completed task's worktree reopen the task: `run`
verifies them before it lands.

## Preparing a worktree

A worktree holds only what git tracks: no `node_modules`, no generated code, no local env files. Say what a
fresh one needs in the repository's `.agentgate.json` (or in your user file):

```json
{
  "worktree": {
    "copy": ["env/.env.local"],
    "setup": [["yarn", "install", "--immutable"], ["npm", "run", "local:prisma:base:generate"]],
    "setup_timeout_s": 1800
  }
}
```

`copy` brings untracked files or directories from your checkout; `setup` commands run once in the new worktree,
outside the sandbox, like the repository's own scripts. They see `AGENTGATE_CHECKOUT` (your checkout) and
`AGENTGATE_WORKTREE`. The checkout's `.agentgate` directory is linked into the worktree, so plans and helper
scripts kept there stay reachable. Settings that are yours rather than the team's go into
`.agentgate/config.json`. A failing setup command stops the start and removes
the worktree. `"default": true` isolates every writing task without the flag.

Do not link `node_modules` from the checkout into the worktree in a workspace monorepo: workspace packages in it
point back at the checkout's sources, and the checks would silently test the wrong code. Install instead — or,
when the checkout's dependencies are current and on the same filesystem, copy them with hard links. This
shares file contents instead of duplicating them and keeps relative workspace links pointing into the worktree:

```json
{"worktree": {"setup": [["sh", "-c", "cp -al \"$AGENTGATE_CHECKOUT/node_modules\" node_modules"]]}}
```

Hard-linked files are shared with the checkout: fine for reading, wrong for a task that changes dependencies.

## Limits

- Only files are isolated. Ports, local databases and running services are shared: two tasks that drive the
  same local stand will disturb each other.
- A worktree with installed dependencies costs time and disk. For a small fix the ordinary mode is cheaper.
- `-w` is for tasks that write code. `review` and `clean` judge what is already in your checkout.
- One session holds one task: land or drop a finished isolated task before starting the next in that session.
- Finished work that nobody landed is never pruned.
