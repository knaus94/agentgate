# Validation record — AgentGate 0.14.0

Release validation on 2026-11-14 against the refactored source based on
`d1185635e89587683acfe90b047a29e0c70e7c72`, including the corrections found during this review.
This record distinguishes live integrations from controlled provider fixtures and does not claim exhaustive
model quality, arbitrary specification size or every possible execution environment.

## Environment and automated checks

| Component/check | Actual result |
|---|---|
| Host | Linux x86-64, Go 1.26.0, Node 24.18.0 |
| Local browser tooling | Installed Playwright core 1.65.0-alpha-2026-10-30; Chrome for Testing 157.0.8103.15 |
| CI browser dependency | Playwright core 1.65.1 remains pinned in package-lock.json |
| Formatting and `go vet ./...` | PASS |
| `go test -race -count=1 -json ./...` after fixes | PASS: 193 top-level tests, 230 passing test events, zero failures/skips |
| Node core suite | PASS: 18 tests |
| Full Chromium workflow | PASS: 6 top-level cases / 8 events, zero failures/skips |

The original imported validation record used a restricted environment where Chromium could not create a
socket. That is no longer an outstanding blocker: the current run launched a real browser. It then exposed
a test-helper defect: duplicate `--root` options were rejected by the newly strict parser. The helper now
supplies the default only when no override is present; the missing-root case asserts its actual diagnostic,
so an argument error cannot masquerade as the expected result.

Browser scenarios cover CSS colour conversion, artwork mapping, image dimensions, missing/changed roots,
baseline drift, mobile/tablet geometry, setup errors and cleanup/cancellation of a real temporary HTTP stand.
The Node core suite also exercises fixture identity, origin/path handling, atomic export, inventory integrity,
REST protocol errors and owned-process cleanup. These do not access a user's backend.

## Real provider-backed CLI smoke

A small isolated repository was processed through the normal Codex CLI (0.167.2): discovery, writer,
independent reviews, declared check, finish and read-only status/dashboard. The actual routes were Terra for
discovery, Astra:medium for writing and Sol:high for review. A local SKILL rule and check-backed requirement
were assessed, four assertions passed and the task reached `complete`. Only its assigned source file changed.

The repository, configuration/state/data directories and credential copies were temporary. Private credential
copies were removed after the run; user task state and installation were not changed. This is a real inference
smoke test, not evidence that models will always implement a large specification correctly.

## Real staged backend workflow

A second isolated fixture exercised a three-stage Go standard-library backend: query parsing/validation,
pagination business logic, then HTTP composition tested with `httptest` (no listening service or database).
Actual writer assignments carried the accepted prior-stage summaries and concrete handoff contracts.
Each stage passed independent review; finishing the third stage opened the required whole-change integration
review instead of immediately completing the task. That review verified all three requirements and the
local backend rule; all three declared Go checks passed and the task reached `complete`.

To limit token use, this fully specified fixture disabled discovery and used one independent combined review
lane, with `max_parallel=1`. It consumed eight live calls: three writers, three stage reviews, one recheck
and the final integration review. The recheck was necessary because the first review correctly marked
insufficient test evidence as `partial` and finish refused. Only the scratch verifier was expanded; the
contract and task state were not weakened. The cheap scripted scenarios below cover additional failures
without more inference calls. Temporary credential copies were removed afterward.

## Coordination, correction and recovery

The following use actual CLI processes, Bubblewrap and filesystem state, with scripted provider responses
so repeated protocol checks consume no model quota:

- Two backend streams with an `after` dependency: the downstream packet contains the upstream handoff.
- A BLOCKED consumer receives its answer in the original provider session; the completed upstream stream
  is not run again.
- A concrete P1 finding is accepted in triage, sent only to the owning writer, fixed in the same session
  and directory, then rechecked with the original finding ID and the correction delta. The task completes.
- A heavy plan retains one accepted whole-task architecture across two stages and a final integration
  review. Each stage is accepted separately and the overall task completes.
- After a failed writer, changing a project rule makes `continue`, repeated `launch` and `answer` refuse
  stale architecture before a writer starts. The saved batch and source baseline remain intact.
- Damaging a peer task's state cannot free its file claims: a second session is refused and the original
  source is unchanged. Normal disjoint sessions still work.
- Read-only `config show`, `validate`, `path`, `profiles` and `extensions` need no writable state. The actual
  Node `readScreen()` path through `config show` also works; absent state directories are not created.

The last three behaviours required production fixes during release validation. State decoding is shared
between current-session and ownership checks; continuation validates the whole Epic's architecture and the
current stage's scope/UI. No retry loop, state-file bypass or weaker acceptance was introduced.

## Live Figma browser export

Using a protected copy of the existing browser profile, the normal CLI successfully read a bounded catalogue
and exported one content frame through `--transport browser`: 73 layers, 12 assets and an 18-file inventory.
The node image is 1360×655, matching its geometry; the context image is 1920×1098. The Go `route` command
accepted the exported package's inventory. The profile copy was removed afterward. No live file was edited.

This validates the browser compatibility adapter against that file at the time of the run. The REST adapter
was tested with controlled responses, not a real token; access to other files or future Figma UI versions
is not implied. Private designs, profile data and URLs are not included in this report or the distribution.

## Distribution

The source-plus-binary archive was independently extracted. Its inventory/checksums and exclusions were
checked; `.git`, node_modules and private profiles are absent. Both Linux architectures were cross-compiled;
the amd64 executable was run. The offline installer succeeded into a path containing spaces. Corrupting its
input binary caused a checksum refusal and preserved the already installed executable. A source rebuild
from the extracted candidate matched the included amd64 bytes.

Final archives are rebuilt from the release source after corrections; the release workflow verifies the
extracted inventory and runs the offline installer again before publishing. ARM64 execution requires actual
ARM64 hardware and was not performed locally.

## Limits and reproduction

Live Claude inference, subscription exhaustion, every fallback model and physical-device/Safari testing
were not performed. Existing controlled tests cover provider events/failures, credential-refresh conflicts,
quota routing, worktree/user-change preservation and terminal input/rendering. The earlier PTY validation
is historical; this follow-up did not repeat that interactive session.

```sh
make check
make design-browser
make package VERSION=0.14.0
```

Use `PLAYWRIGHT_CORE` or `AGENTGATE_BROWSER_EXECUTABLE` when selecting already installed browser tooling.
CI installs its pinned dependency. Do not use tests to modify a live project's task state.

Evidence:

- [Current Go race events](validation/go-tests.jsonl)
- [Current browser output](validation/node-browser-current.log)
- [Current Node core output](validation/node-core-current.log)
- [Coordination and live-integration summary](validation/followup-scenarios.json)
- [Original restricted-browser attempt](validation/node-browser.tap)
- [Original archive/source checks](validation/archive-verification.log)
