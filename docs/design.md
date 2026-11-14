# Design

[Русский](ru/design.md)

A stream can implement a Figma design, and the gate can tell whether it did. What can be computed is computed
by tools; a model reads short results.


## Coordination

For phone and tablet references, declare `device: "mobile"` and the intended CSS `viewport` in each design
entry (or in `screen.json`). A narrow desktop window is still a desktop: classic scrollbar gutters can
reduce its document width. Tablet checks use the same mobile mode with their own viewport. This is Chromium
mobile/touch emulation at one screenshot pixel per CSS pixel, not a physical-device or Safari test.

```json
{"design": [{"screen": ".agentgate/design/mobile", "root": "main.app-container",
             "viewport": "390x844", "device": "mobile", "stream": "web"}]}
```

Match the exported frame's border box, including padding. A 390px content frame with 12px padding matches
a 390px wrapper, not its 366px child. The wrapper may belong to the existing shell: measuring its content
region does not require changing shell CSS. Do not add viewport pixels or raise tolerances to offset a gutter.
Reports include the effective device, viewport, document width, root size/padding and horizontal overflow.
Use those observations to distinguish a wrong comparison boundary from a real layout defect.

For overlapping artwork, `map.select` maps a layer handle to a unique CSS selector inside the selected root.
It selects the actual element even if its position is wrong; geometry/style checks still apply. Ambiguous
selectors are setup errors. The compact element labels used by older maps remain supported when unique.


Start with `agentgate prepare --file <JSON with request>`. The tool reads a bounded Figma catalogue and a
read-only worker selects content nodes, discovers the repository and saves the plan. The lead handles product
questions and authorization, not exports, CSS inspection or screenshot comparison. Writers receive packages
and source checks; the gate owns layout commands, baseline files and stand lifecycle. It sends measured
failures back through `fix`, and an independent reviewer checks pictures.

`agentgate layout` resolves `design.stand` through the same configuration as preparation, including extensions
and personal checkout overrides. It uses the selected screen's repository even when invoked from elsewhere.
Direct standalone Node use reads `.agentgate.json` only.

`layout measurements: PASS` means only that the measured properties pass, including any unchanged baseline
differences. The existing reviewer must also assess four built-in blocking rules per design: appearance
(including crop), placement in the shell, control semantics, and exceptions/viewport/state coverage. This
applies even without plan requirements. Missing pictures or other required evidence prevent completion;
these rules cannot be waived as `not_applicable`. No extra reviewer is started.

Packages retain the parent screen as `context.png`; layout saves the full implementation as `page.png` next
to the cropped `actual.png`. Their coordinates and pictures preserve the header-to-content relationship.
Figma references are exported at the node's absolute bounds so overflow cannot shift the comparison's origin
([Figma export settings](https://developers.figma.com/docs/plugins/api/ExportSettings/)). Older linked packages
without screen context are reimported by the gate; this never captures a new implementation baseline.

For a requested responsive range, preparation proposes reference widths and relevant intermediate/breakpoint
observations from the repository's layout rules. Only approved scenarios run. Two endpoint screenshots and
no overflow do not establish behavior throughout the range. Control semantics and unavailable-data states
come from the accepted task and [project rules/skills](extensions.md#repository-frontend-skills), not the
appearance of a still image or a universal HTML/CSS prescription.

Use `replan --plan <name>` with an update reason to correct an idle task's measurement contract. This retains
its original source snapshot and findings; it regenerates managed layout checks and invalidates review.
No new baseline is captured after implementation. An absent/mismatched baseline makes all differences count.
Only an identical discrepancy under the same design, root, viewport, map and tolerances is grandfathered;
changed discrepancies remain visible. Exit 1 means a measured difference; exit 2 means verification could not
run and must be repaired before asking a writer to change CSS.

## The flow

| Step | Who | What happens |
|---|---|---|
| Sign in | you, once | `agentgate figma login` opens a browser window. The session stays in a profile under `~/.local/share/agentgate/private`, which no worker sees. No token and no cookie file |
| Choose | preparation worker | receives the bounded catalogue and selects matching content nodes |
| Pull | the gate | imports the selected design at launch |
| Plan | preparation worker, then lead | drafts the plan; the lead accepts scope and authorizations |
| Measure | the gate | before anyone writes, the page is compared with the package: what differs already is not the task's |
| Write | the stream's writer | reads the brief, about two thousand tokens for a page section, and runs its declared source checks |
| Check | the gate | the layout check again, as a declared check. A new difference blocks the review |
| Judge | the first reviewer | is shown the design next to the page and looks for what numbers do not carry |

## The package

| File | Read by | Holds |
|---|---|---|
| `brief.md` | the writer, the reviewer | the layers as intent: layout rules, type, colour, design tokens |
| `frame.png` | the writer, the reviewer | the picture of the node |
| `context.png` | the writer, the reviewer | the parent screen when the selected node is a section |
| `assets/` | the writer | icons as SVG, pictures as PNG |
| `manifest.json` | the gate | source, file version and a digest of the content: a design pulled again makes earlier reviews stale |
| `data/` | tools only | the node tree in Figma's REST format |

A component instance is one line of the brief, repeated siblings are one item of a list, and positions that
follow from auto layout are not printed. `agentgate figma node <dir> <handle>` prints one layer in full.

## What is caught, and by what

| Difference | Caught by |
|---|---|
| position, size, radius, font, colour, a missing element | the layout check, without a model |
| a wrong icon, a lost gradient or shadow, clipped text | the reviewer of the `design` axis, from two pictures |
| an exception that excuses what the product does not need | the same reviewer, reading the map |
| behaviour | the correctness reviewer, the project's tests and, when opted in, browser verification |

The layout check finds a text by its words and a painted box by where it lies, so the code needs no markup. It
judges a layer against its matched parent: a panel that moved is one finding, not one per child.

## A screen and its stand

A screen is a directory, and a project describes its front end once:

```
.agentgate.json                design.stand: how the front end starts, once per project
<screens>/<screen>/screen.json where the screen is: url, root, and optionally locale, viewport, cookies
<screens>/<screen>/package/    the design, written by `agentgate figma pull`
<screens>/<screen>/fixtures/   recorded GraphQL answers: the state the screen is shown in
<screens>/<screen>/map.json    what the product shows differently
```

```json
{ "command": ["yarn", "nx", "run", "client:dev"], "ready": "http://localhost:4200",
  "env_file": "env/.env.local", "graphql_env": ["VITE_PUBLIC_GRAPHQL_ENDPOINT", "GRAPHQL_INTERNAL_URL"] }
```

`agentgate layout <screen>` is the whole of it. When nothing answers at `ready`, it serves the screen's
fixtures as the GraphQL endpoint, starts `command` with the variables of `graphql_env` pointing there, waits,
compares, and stops everything it started; the screen needs no backend behind it. In a plan the screen is
`design [{screen, stream}]`, and the gate runs the same command. `agentgate layout <screen> --record
<gateway>` runs against a real gateway instead and keeps its answers as the fixtures.

What the product deliberately shows differently is named in the screen's `map.json`, each entry with its reason:
`{"ignore": {"<handle>": "why"}, "dynamic": ["<handle>"]}`.

## Limits

`figma pull` works with view access by reading the Figma web application's own scene graph, because the REST
API and the MCP server allow a view seat a few calls a month. That graph is not a published interface: when
Figma changes it, the command stops and says which part is gone. A Dev seat makes the official API usable.
