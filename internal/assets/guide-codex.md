
## Leading from Codex

You are a Codex session. Everything above applies; these points are specific to you and replace the "Waiting"
section.

- **One plain command.** `agentgate` starts its own sandbox for the workers, reaches the model providers and
  keeps its state under `~/.local/state`, so it has to run outside your sandbox. The rule installed with it
  allows exactly that and matches only a simple command: `agentgate <action>`, `agentgate <action> --file <path>`
  or `agentgate <action> --plan <name>`. A pipe, a heredoc, `&&`, `;`, a redirection or a wrapping script on
  the same line keeps it inside your sandbox, and it answers "running inside the Codex sandbox".
- **Inputs are files.** Wherever the guide says `--file -`, write the JSON to a file first (`$TMPDIR` is
  writable), then pass its path. Do both in one `exec` script — the file write, then the command — so an input
  costs one turn, not two. The task text goes into a file the same way and never onto a command line.
- **Waiting costs a turn every time you look.** `prepare`, `architecture`, `start`, `run`, `answer` and
  `decide` run model workers for minutes and print nothing until they end. Wait like this, and no other way:

  ```js
  // @exec: {"yield_time_ms": 300000}
  let r = await tools.exec_command({cmd: "agentgate run", yield_time_ms: 30000});
  while (r.session_id !== undefined) {
    r = await tools.write_stdin({session_id: r.session_id, chars: "", yield_time_ms: 300000});
  }
  text(r.output);
  ```

  `exec_command` yields after at most 30 seconds. An empty `write_stdin` poll may wait **300000 ms** and
  returns as soon as the command ends, so a long wait costs nothing extra. The pragma on the first line lets
  the script itself wait that long; without it the script yields after 30 seconds. If it still yields with a
  cell id, continue it with `wait` and `yield_time_ms: 300000`. Never poll with a short `yield_time_ms`, never
  call `agentgate status` to see whether a command is still running, and do not start a second `agentgate`
  command while one runs: it would be rejected, not queued. When your shell tool returns only after the
  command has ended, as it does in a non-interactive session, there is nothing to wait for: call the command
  once.
- **The workers are agentgate's.** Do not use your own sub-agents, `codex review` or plan mode for this task.
- **User questions.** Use the native input tool of this session (`request_user_input`, or
  `request_user_input_async`: its acknowledgement means the question is pending, not answered). If it is
  unavailable in the current mode, ask in one concise message; do not enter plan mode or build a workaround.
  Some Codex versions expose it in Default mode through `features.default_mode_request_user_input`; that is
  the user's setting, not something to edit during a task.
- **Ending a turn.** The user may have trusted the hooks (`/hooks`): one holds you to an unfinished
  task. Do not rely on it: before you end a turn, the last `agentgate` answer must say `done`, or you `pause`
  with the true reason.
- The user's command is `$dev`, where this guide writes `/dev`.
