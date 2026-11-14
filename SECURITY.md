# Security and trust model

AgentGate is a local developer tool for trusted projects, provider CLIs and configured extensions. Its
quality gate prevents several classes of accidental completion and unsafe local state transitions. It is
not an isolation service for hostile repositories or untrusted arbitrary code.

## What is enforced

- Workers run through bubblewrap with a read-only host mount, private temporary/runtime paths, and only
  declared writable mounts. Writers receive a writable checkout; ordinary reviewers do not.
- Lead-private browser data is masked after all other mounts. A later ancestor mount cannot reveal it.
- Explicit MCP writable paths are validated; the entire checkout, parent traversal and Git metadata cannot
  be granted through a cache-directory declaration.
- Worker credentials use private snapshots rather than a writable mount of the real login file. Atomic
  provider refreshes are supported and reconciled with conflict checks. Configured account selection strips
  ambient provider credentials that could override it. Figma API tokens are lead-only environment values.
- Invalid stream/session/path components are refused before constructing state or worker paths.
- Completion requires current scoped evidence, completed streams, successful required checks and no
  blocking findings. Historical author models are excluded from review.
- Worktree landing checks its scope. A commit landing refuses unrelated staged entries instead of including
  them in the task commit. Runtime cancellation identifies processes by PID and start identity.
- Configuration, state, release binaries and design packages use guarded replacement rather than truncating
  the active copy. Updater/installer downloads require an exact expected checksum entry.
- Terminal log text is sanitized to prevent ANSI/OSC control injection. Fixture inputs cannot traverse paths,
  follow fixture-file symlinks or silently fall through to a live backend in replay mode.

## What is not guaranteed

**Read access is broad.** The host read-only mount is still readable, and workers need network access for
provider APIs and some development tools. It is not a confidentiality boundary for every host file or every
environment variable. The lead-private mask protects AgentGate's private directory; it does not claim to
hide all unrelated user secrets. Run untrusted code in a separate VM/container/account with its own network
and credential policy.

**Write scope inside one checkout is audited, not mounted per file.** A writer can physically edit a sibling
file in its checkout. The gate audits ownership and prevents acceptance of foreign work; it cannot undo
an arbitrary malicious command or promise rollback of external side effects.

**Declared checks and extension refresh commands are executable authority.** In writing tasks they may run
with the lead's local environment. Review-only checks are confined. Grant commands and MCP tools only when
the task and project authorize them. No prompt text converts a local developer tool into an OS policy engine.

**Evidence has an explicit scope.** Review hashes cover declared task/context and required proof inputs, not
all files a model could have read. Check input globs are part of the contract. A narrow or incomplete input
list can omit a real dependency; choose it deliberately, or omit it to use repository-wide check freshness.

**Provider and browser behavior is external.** A recorded model report is not proof that its claims are true.
That is why checks and independent reviews remain required. Figma's internal browser adapter may break
when its web app changes; the documented REST transport is available separately. Chromium layout tests
are not Safari, physical-device or cross-browser certification.

**Credential concurrency has limits.** Snapshot writeback uses AgentGate locking and Codex's documented
profile lock where applicable. Other programs that ignore the relevant lock can still race. Conflicting
updates are surfaced and copies retained rather than silently deciding which login wins.

## Reporting a vulnerability

Report reproducible security issues privately to the repository owner. Include the affected version,
platform, command, expected behavior and a minimal reproduction with synthetic data. Do not include real
API keys, OAuth tokens, browser profiles, private repository contents or customer data.

The [bubblewrap project](https://github.com/containers/bubblewrap#sandbox-security) explains why a sandbox's
security properties depend on its configured mounts and namespace policy. AgentGate's concrete boundary is
the one described above, not an absolute claim of safety derived from using bubblewrap.
