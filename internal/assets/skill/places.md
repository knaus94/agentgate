# Where things are

| What | Where | How you read it |
|---|---|---|
| saved plans | `.agentgate/plans/<name>/plan.json` and `brief.md` | `brief.md` is for people; change `plan.json` and `agentgate plan save <name> --file …` again |
| configuration | `~/.config/agentgate/config.json`, `.agentgate.json`, `.agentgate/config.json` | `agentgate config show`, `agentgate modes` |
| the task: position, findings, decisions | the tool's state | `status`, `show <part>`, `watch` |
| a writer's work | the checkout, or the task's worktree (`isolated.worktree`) | git |
| the patch to review, check logs | the paths `show review` and `show checks` name | read them |
| the variables the checks need | the private file the plan's `env_file` names, outside the repository | yours; the tool shows names, never values |
| files the user gave (`attachments`) | copied into the preparation and the task | `status --full` names them |
| design screens | `design.screens` (default `.agentgate/design`) | the tool writes them; writers read their package |

## Never

- Never edit, rename or delete anything under `~/.local/state/agentgate`, and read only two things there: an
  isolated task's worktree (`isolated.worktree`), with git, and the check logs and patches `show checks` and
  `show review` name. `status`, `show` and `watch` read the rest, `drop` and `forget` remove a task,
  `pause {cancelled: true}` ends one. A refusal is settled by fixing its cause, never by editing state or
  recreating a task.
- Never call the design kit's scripts, the Figma exporter or the layout checker yourself. `agentgate figma …`
  and `agentgate layout …` are the user's tools; the task runs them.
- Never run a worker's CLI, `codex review`, sub-agents or plan mode for the task: the workers are agentgate's.
- Never parse an answer with `jq`, `python` or `sed`: every answer is written to be read, and `show` has the
  rest.
- Never put the user's request on a command line: it goes into a file, as it was typed.
