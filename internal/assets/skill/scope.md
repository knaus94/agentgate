# A writer needs a file outside its stream

A writer edits only its stream's files. One that needs more stops with `BLOCKED` and names the file: you see
`needs: answer`.

- **The file belongs to the task.** Give it to the stream, then answer the writer. `amend`:

  ```json
  {"update": "the limit lives next to the handler", "scope": [{"stream": "api", "add": ["src/orders/limits.ts"]}]}
  ```

  then `answer`:

  ```json
  {"stream": "api", "answer": "src/orders/limits.ts is yours now: put the limit there."}
  ```

  `stage` in a scope change names a later stage instead of the present one. An entry with a trailing slash
  (`prisma/migrations/`) owns everything below it; a pattern that names a directory (`migrations/*_orders`)
  owns what is inside the directories it matches; `*` stays within one name and `**` crosses directories.
- **The file belongs to another stream of the same stage.** Answer the writer to leave it, and say what that
  stream provides; or make the stream wait for the other with `after` (the complete plan through `amend`).
- **The file must not change.** Answer so, with the reason.

Two streams of one stage may never own the same file: `amend` refuses a scope that would, naming both
entries. In a shared checkout another session's task may own the file: the refusal names that session. In an
isolated task a file changed outside every stream stops verification (`needs: scope`).

A generator's output with a name nobody can know beforehand is owned by its directory. A file git ignores is
tracked only when a stream names it exactly.
