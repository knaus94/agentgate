# Research and design decisions

Reviewed for this refactor on 2026-11-14. This is a comparison of published interfaces and implementation
patterns, not a benchmark or a claim that competing products were run against this repository.

## What AgentGate is responsible for

The repository implements a local supervisor around Claude Code and Codex: task planning, disjoint writer
streams, checks, independent review, correction, recovery and evidence-bound completion. It also embeds
optional Figma/layout/fixture tools. The provider CLIs remain responsible for inference, provider sessions
and authentication protocols. AgentGate should not create a second chat runtime or duplicate a provider's
entire permission engine.

## Primary sources and resulting decisions

| Reference | What was relevant | Decision in this implementation |
|---|---|---|
| [Aider Git integration](https://aider.chat/docs/git.html) | A coding tool must account explicitly for existing working-tree changes and commit boundaries | Capture task scope; guard landing against foreign staged files and user hunks; keep uncommitted apply distinct from commit |
| [Aider lint/test workflow](https://aider.chat/docs/usage/lint-test.html) | Executable checks are part of the edit/correct loop | Declared commands, exit status, logs and content-bound receipts; an old failure is not a current success |
| [OpenCode agents](https://opencode.ai/docs/agents/) and [permissions](https://opencode.ai/docs/permissions/) | Roles have distinct instructions and capabilities; permission policy is explicit | Separate preparation, authorship and review; preserve actual model identity across fallback; state the limits of filesystem grants |
| [Command Line Interface Guidelines](https://clig.dev/) | Discoverable help, predictable errors, machine-readable output and terminal-aware behavior | Command registry, pure help, strict parsing, grouped aliases, JSON snapshot and one-shot pipe behavior |
| [Bubble Tea architecture](https://github.com/charmbracelet/bubbletea) | State, update and view are separate responsibilities in a terminal application | Separate dashboard snapshot, navigation and rendering; retain the small dependency-free implementation rather than add a framework |
| [Bubblewrap README](https://github.com/containers/bubblewrap/blob/main/README.md) | Namespace primitives only enforce the policy that their caller actually specifies | Document write confinement honestly; mask private data after all mounts; check writable real paths; do not claim network isolation |
| [Codex CLI](https://developers.openai.com/codex/cli/reference/) and [non-interactive execution](https://developers.openai.com/codex/noninteractive/) | The installed CLI is the protocol boundary | Preserve session identity and lane directory; validate terminal events/results; surface incompatible protocol responses |
| [Codex authentication storage source](https://github.com/openai/codex/blob/main/codex-rs/login/src/auth/storage.rs) | Login refresh uses file replacement and coordination | Private worker snapshots support atomic refresh; guarded writeback uses the account lock and preserves concurrent changes |
| [Claude Code CLI](https://code.claude.com/docs/en/cli-reference) | CLI flags, structured output and session continuation define the adapter contract | Separate Claude adapter and event parsing from routing, sandbox setup and generic process lifecycle |
| [Figma file/image endpoints](https://developers.figma.com/docs/rest-api/file-endpoints/) and [rate limits](https://developers.figma.com/docs/rest-api/rate-limits/) | Supported exports have permissions, version parameters and plan-dependent limits | Add explicit REST transport, pin render version, bound work and report rate errors; retain private browser access only as a compatibility adapter |
| [Semantic Versioning](https://semver.org/) | Version precedence includes prereleases and ignores build metadata | Automatic update requires a strictly newer valid version; a locally delivered newer build cannot be downgraded by upstream latest |

These references informed behavior and boundaries. They did not justify copying another product's UI,
automatically committing a user's unrelated work, removing existing integrations, or ranking models using
unverified scores. Model aliases and effort are configuration, with independence enforced separately.

## Architecture choices

### One local binary, focused internal modules

A daemon, database and web application would add deployment and authentication surfaces without solving a
confirmed need in this repository. The tool remains an ordinary Go CLI. Each invocation owns an operation,
uses explicit state files/locks and exits. Local Claude/Codex integrations can keep calling the same
structured protocol. The optional Node design subsystem remains independently testable and embedded into
the binary by content hash.

Monolithic files were divided along failure and ownership boundaries: command parsing; task lifecycle;
snapshots/check receipts; worktree landing; terminal state/update/view; provider protocol; process/sandbox;
credentials; quota management; configuration loading/validation/storage; and distribution. Public behavior
was preserved where it was coherent, with unsafe acceptance semantics corrected deliberately.

### Evidence follows its declared inputs

A check without declared inputs can depend on the whole repository, so its receipt binds to working content
and HEAD. Explicit input patterns enable narrower reuse. Review binds to the owned files, declared context,
project guidance and required evidence. Automatically binding every review to every repository file would
invalidate concurrent tasks for unrelated edits. Users needing a stable full checkout should use a task
worktree. These are deliberate differences, documented in [architecture](architecture.md).

### Failures remain visible

A failed baseline cannot establish that a later failed command is the same defect. A missing fixture cannot
establish a legitimate empty API response. A corrupt task state cannot establish that no task exists.
A reused PID cannot establish ownership of a live process. These distinctions became enforced conditions
and regression scenarios, rather than optimistic fallbacks.

### Compatibility with an explicit migration path

Flat commands, valid plans, configuration layering and existing integrations remain available. `task` and
`setup` organize discovery without forcing script rewrites. Strict validation rejects inputs that were
previously ignored. An interrupted task can resume its saved round without discarding evidence. Browser
Figma remains available, while an official API route has a clear selection rule and no hidden fallback.
See [migration](migration.md) for the observable changes.

## Limits of the research and tests

Source/documentation review is not a substitute for provider access. Local integration tests use scripted
CLI executables; no subscription quota or paid model API was consumed. REST behavior is checked using
controlled protocol responses, not a user's private Figma file. Live compatibility depends on the installed
CLI, model access and external service version. The exact executed test record is in
[validation](validation.md), including the Chromium limitation of the build environment.
