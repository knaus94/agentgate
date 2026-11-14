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

For unresolved user decisions, prefer the native `request_user_input` / `request_user_input_async` tool
available in this session, following the guide. Respect its mode restrictions; unavailable or unanswered
forms fall back to a concise chat question, never an assumed answer.

`agentgate` has to run outside your sandbox, and the rule that allows it matches only a plain command. Call it
as one simple command every time — `agentgate <action> --file <path>` — with no pipe, heredoc, `&&`, `;` or
redirection on the same line. Inputs go into a file first, written by a separate command.

For repository discovery or a Figma task, call `agentgate prepare --file <JSON with request>` first. Its
read-only worker investigates the repository and prepares the saved plan; the host tool reads Figma without
exposing the session to workers. The lead reviews scope and authorizations, not source, design exports or
screenshots. Follow the returned next step and `agentgate guide`.
