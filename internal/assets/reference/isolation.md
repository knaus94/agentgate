# agentgate reference — isolated tasks

With `-w` in the request, with `worktree.default` in the configuration, or for a plan of several stages when
the repository says how to prepare a worktree, the task works in its own git worktree on its own branch,
created from the checkout's `HEAD` when the task starts. The flow is the same, with these differences.

- The user's checkout does not change, and you do not change it: never edit, stage or check out anything there
  for this task. Every answer carries `isolated {worktree, branch}`; read files and diffs at that path
  (`git -C <worktree> diff`). The user's uncommitted edits are not in the worktree.
- Tasks of other sessions may touch the same files; nothing is claimed.
- Every accepted stage is a commit on the task's branch. A change outside the task's scope refuses that commit:
  give the file to a stream with `amend`.
- At `done` the checkout is still untouched. Report what was done, show the user the diff, and ask how to bring
  it in. Then, on their word only, `agentgate land` `{how}`:

  | `how` | Result |
  |---|---|
  | `apply` (default) | the change appears in the checkout, uncommitted |
  | `commit` | the same, committed on the current branch with your `message`, in the repository's convention |
  | `branch` | the checkout is not touched; the task's branch is kept for a pull request |
  | `drop` | the work is thrown away; possible at any step |

- If the checkout moved on, `land` merges it into the worktree first. It may answer `landed: false` with the
  task reopened: follow its `needs`. Conflict markers go back to the writers; `decide {brief}` says how the two
  changes combine. Then `run`, and `land` again.
- `land` refuses when the user has uncommitted edits in the files it would write. Tell them; do not stash or
  discard anything of theirs.
- Preparing a worktree (`worktree.copy`, `worktree.setup` in the configuration) is the repository's business.
  If a check fails because the worktree lacks dependencies or generated code, say so and `pause`: do not
  improvise installs.
