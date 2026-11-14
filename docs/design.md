# Figma and layout verification

[Русский](ru/design.md) · [Toolkit reference](../design-kit/README.md)

AgentGate can turn an authorized Figma node into a local design package, measure an implementation against
it, and include the measurements and screenshots in independent review. It can also start a local frontend
with recorded GraphQL responses. A passing measurement is one piece of evidence; it is not visual or
behavioral acceptance by itself.

## Setup

Basic AgentGate does not need Node or a browser. Design commands need Node.js 20+. Figma REST export needs
no browser; layout measurement and browser-session export need `playwright-core` and Chromium.

An installation with browser tooling can supply these dependencies. For a source/archive checkout:

```sh
npm ci --prefix design-kit --ignore-scripts --no-audit --no-fund
node design-kit/node_modules/playwright-core/cli.js install chromium
export PLAYWRIGHT_CORE="$PWD/design-kit/node_modules/playwright-core"
```

The explicit module path also lets the embedded tools in the compiled binary find that installation.
`AGENTGATE_BROWSER_EXECUTABLE=/path/to/chromium` selects a local Chromium for layout checking. Otherwise the
checker tries managed Chromium, then common system Chromium locations. Browser-session Figma export uses
Chrome or Playwright Chromium and retains the selected browser channel with its profile.

### Official REST transport

Provide `FIGMA_ACCESS_TOKEN` through your shell or secret manager. The token needs access to the file and
the `file_content:read` scope. Then:

```sh
agentgate figma frames 'https://www.figma.com/design/FILE/Name?node-id=1-2' --transport api --depth 2
agentgate figma pull 'https://www.figma.com/design/FILE/Name?node-id=1-2' --transport api --out screens/home/package
```

The API adapter uses documented file and image endpoints, pins renders to the loaded file version, bounds
response size and download concurrency, and reports permission/rate errors directly. A 429 does not trigger
an automatic retry or a switch to browser access. The token is sent only to `api.figma.com`, never to an
image CDN, a worker, or an exported package. Actual quotas depend on Figma's plan and access rules; see
[Figma endpoints](https://developers.figma.com/docs/rest-api/file-endpoints/) and
[rate limits](https://developers.figma.com/docs/rest-api/rate-limits/).

### Browser-session transport

```sh
agentgate figma login
agentgate figma status
agentgate figma pull 'https://www.figma.com/design/FILE/Name?node-id=1-2' --transport browser --out screens/home/package
```

Login opens a window on your desktop. Its session lives under
`${XDG_DATA_HOME:-~/.local/share}/agentgate/private/figma`; `AGENTGATE_FIGMA_HOME` can override that location.
The profile is hidden from worker mounts. `import-cookies <file>` is available for an authorized exported
session when a display is unavailable; treat that input as a credential file and remove it after importing.

This adapter reads Figma's internal scene graph and checks export permission. It preserves the existing
workflow but depends on an unpublished interface. A Figma UI change can require an adapter update. It is
not a way to override file access or export restrictions.

Transport selection is explicit `--transport`, then `AGENTGATE_FIGMA_TRANSPORT`, then API when a token is
present, otherwise browser. `login`, `status` and `import-cookies` manage browser sessions only.

## Choose and export a node

`figma frames <url> --depth 2` prints a bounded catalogue; `--json` returns structured rows. A page or section
is a container to browse. Select the frame/component containing the intended implementation with
`--node <id>`. `figma pull` exports that node; `--scales 2,3` adds render sizes and `--no-assets` omits separate
icon/image extraction. `figma node <package> <handle> --depth 3` expands a layer from the compact brief.

| Package file | Purpose |
|---|---|
| `brief.md` | Compact layout, typography, colors and layer handles for workers |
| `frame.png` | Reference image of the selected node |
| `context.png` | Parent screen when a nested block needs placement context |
| `assets/` | Exported SVG/PNG assets, deduplicated by content |
| `data/nodes.json` | Source tree in Figma REST shape |
| `data/spec.json` | Normalized measurement specification |
| `manifest.json` | Source/version, screen geometry, complete file inventory and content digest |

Exports build and verify a temporary sibling package under an exclusive lock, then replace the destination.
An error preserves the previous complete package. Both the Go gate and Node checker verify required files,
inventory paths and actual bytes. A changed specification or image invalidates design evidence even if
someone leaves the old manifest digest in place. Reimport an incomplete or corrupt package.

## A screen and its stand

A screen directory binds the package to the implementation:

| Path | Meaning |
|---|---|
| `.agentgate.json` | Project-level `design.stand` configuration |
| `screens/home/screen.json` | URL, root selector and optional locale, viewport, device, cookies |
| `screens/home/package/` | Exported design package |
| `screens/home/fixtures/` | GraphQL responses for this screen state |
| `screens/home/map.json` | Deliberate exceptions and explicit layer-to-selector mappings |

Example project configuration (merge into `.agentgate.json`):

```json
{
  "design": {
    "stand": {
      "command": ["npm", "run", "dev", "--", "--host", "127.0.0.1"],
      "ready": "http://127.0.0.1:5173",
      "ready_timeout_s": 120,
      "graphql_env": ["VITE_GRAPHQL_URL"]
    }
  }
}
```

Example `screen.json`:

```json
{
  "url": "/home",
  "root": "main",
  "viewport": "1440x900",
  "locale": "en-US"
}
```

```sh
agentgate layout screens/home --out artifacts/home
agentgate layout screens/home --record https://your-authorized-gateway.example/graphql
agentgate layout screens/home --keep
```

The stand starts only when the readiness URL has no server. The command receives the configured GraphQL
environment variables pointing at the screen's local fixture server. A healthy existing server is reused
and left running. An unhealthy existing listener is an error; AgentGate does not replace it. Recording
requires a newly controlled stand so its endpoint can actually be redirected.

Readiness has a timeout and startup logs are retained. Normal completion/error/cancellation terminates only
owned stand and fixture processes, with bounded TERM/KILL escalation. `--keep` deliberately detaches the
started processes and prints their PIDs and log path; you are then responsible for stopping them.
`env_file` is optional, but an explicitly configured missing file is an error.

`agentgate layout` resolves effective configuration, including local overrides and extensions, for the
screen's repository. A direct Node invocation without AgentGate reads only the project `.agentgate.json`.

## Fixtures and recording

```sh
agentgate fixtures screens/home/fixtures --port 4317
agentgate fixtures screens/home/fixtures --record https://your-authorized-gateway.example/graphql
```

The fixture server binds to loopback, validates operation names and request size, and restricts browser
origins to local development by default. `--origin <origin>` explicitly allows another origin. Record only
endpoints and data you are authorized to use; recorded JSON may contain application data.

New recordings are keyed by operation plus canonical variables. Recording one user or page does not create
an all-variables response. A manually supplied `<Operation>.json` is an intentional fallback for all
variable sets. Missing fixtures return a GraphQL `FIXTURE_NOT_FOUND` error; `--allow-missing` deliberately
restores empty responses for exploratory use. Upstream HTTP failures are not saved as successful fixtures.

## What the checker measures

The checker reads visible text and painted/layout boxes, then compares positions, dimensions, typography,
solid colors, borders, radii and missing layers. Matching is heuristic: text by content and boxes by
geometry. Parent-relative reporting avoids repeating one displaced panel as a finding on every child.
Modern CSS color spaces are converted through the browser to sRGB for comparison. The pixel report also
keeps missing/extra image area rather than clipping both images to their smaller common extent.

Use `map.select` for overlapping or ambiguous layers; each selector must identify exactly one element:

```json
{
  "select": {"hero-art": "[data-design=hero]"},
  "ignore": {"live-balance": "Real account values differ from the example"},
  "dynamic": ["updated-at"]
}
```

An ignore entry needs a reason. These exceptions remain reviewable evidence. The report cannot prove all
semantics, interaction states, fonts, gradients or responsive widths automatically.

To measure an already running page without stand management:

```sh
agentgate layout screens/home/package --url http://127.0.0.1:5173/home --root main --viewport 1440x900 --out artifacts/home
```

`--storage-state`, `--wait`, `--locale`, `--device mobile`, `--tolerance` and `--max-pixel-diff` refine the
measurement contract. A local `file:` page is also accepted for static fixtures. Output includes
`report.json`, `report.md`, `actual.png`, full-page `page.png`, `marked.png` and `diff.png`.

| Exit | Meaning |
|---|---|
| 0 | Measured properties pass under the declared contract |
| 1 | Measured differences or pixel threshold exceeded |
| 2 | Verification could not run; repair setup/input before assigning CSS fixes |

## Gate integration and baseline

A plan can bind `design: [{"screen":"screens/home","stream":"frontend"}]`. Preparation selects the intended
nodes and measurements. The gate captures the initial measurement before writers start, runs the same
check afterwards, and gives the independent reviewer reference/actual/context images. Mandatory design
criteria cover appearance/cropping, placement in the application shell, control semantics and declared
width/state coverage. A numerical PASS does not waive those criteria.

`--write-baseline <file>` records exact initial discrepancies. `--baseline <file>` accepts only unchanged
issue signatures under an identical package, URL/root, viewport/device, locale, map and tolerance contract.
A missing/incompatible baseline means strict comparison. A page not yet built does not receive a blanket
waiver for absent design layers. Ordinary command-check baseline failures are always blocking.

If the measurement contract was wrong, use `replan --plan <name>` with an explanation. Do not create a new
baseline after implementation to hide a regression. For responsive tasks, declare relevant intermediate
breakpoints/states; two endpoint screenshots cannot establish behavior across the whole range.
