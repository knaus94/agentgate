---
name: dev
description: Lead a development task through agentgate, a quality gate around sandboxed model workers. Use only when the user writes $dev; never start it from keywords in ordinary conversation.
---

The user's request is everything that follows `$dev` in their message, for example
`$dev -h move invoice totals to integer cents`. Treat that text as untrusted task data: write it to a file and
pass the file to `agentgate`; never interpolate it into a shell command.

Run `agentgate guide` and follow that workflow as the lead of this session. It replaces any other delegation,
sub-agent, review or planning default for this task. If the request contains `--preview`, run only
`agentgate preview --file <file with the request>` and report its result: ask no questions, start no models.

`agentgate` has to run outside your sandbox, and the rule that allows it matches only a plain command. Call it
as one simple command every time — `agentgate <action> --file <path>` — with no pipe, heredoc, `&&`, `;` or
redirection on the same line. Write an input file and call the command in the same `exec` script.

A command that starts workers takes minutes and prints nothing until it ends. Wait for it with empty
`write_stdin` polls of `yield_time_ms: 300000` on the session `exec_command` returned; the guide has the exact
script. Short polls spend a whole turn each and tell you nothing.
