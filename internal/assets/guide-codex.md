
## Leading from Codex

You are a Codex session. Everything above applies; these points are specific to you.

- **One plain command at a time.** `agentgate` starts its own sandbox for the workers, reaches the model
  providers and keeps its state under `~/.local/state`, so it has to run outside your sandbox. The rule installed
  with it allows exactly that and matches only a simple command: `agentgate <action> --file <path>` or
  `agentgate <action> --plan <name>`. A pipe, a heredoc, `&&`, `;`, a redirection or a wrapping script on the same
  line keeps it inside your sandbox, and it answers "running inside the Codex sandbox".
- **Inputs are files.** Wherever this guide says `--file -`, write the JSON to a file first, in a separate
  command (`$TMPDIR` is writable), then pass its path. The task text goes into a file the same way and never
  onto a command line.
- **Operations take minutes.** `architecture`, `launch`, `review` and `fix` run model workers. Give the command
  a timeout that covers the mode's limits (`agentgate modes` lists them), wait for it, and do not start a
  second `agentgate` command while one is running: it would be rejected, not queued.
- **The workers are agentgate's.** Do not use your own sub-agents, `codex review` or plan mode for this task.
  You read the diffs and make the decisions.
- **Ending a turn.** The user may have trusted the Stop hook (`/hooks`), which holds you to an unfinished task.
  Do not rely on it: before you end a turn run `agentgate status` and follow its `next`, or `hold` with the true
  reason.
- The user's command is `$dev`, where this guide writes `/dev`.
