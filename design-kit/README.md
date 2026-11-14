# AgentGate design toolkit

The optional Node subsystem exports Figma packages, measures local pages and serves reproducible GraphQL
screen states. AgentGate embeds the source and exposes it as `agentgate figma`, `agentgate layout` and
`agentgate fixtures`. The same scripts can run directly with Node 20+.

```sh
node bin/figma.mjs --help
node bin/check.mjs --help
node bin/graphql-fixtures.mjs --help
```

## Setup

```sh
npm ci --ignore-scripts --no-audit --no-fund
node node_modules/playwright-core/cli.js install chromium
```

Figma REST export needs only Node and an authorized `FIGMA_ACCESS_TOKEN` with `file_content:read`.
Browser-session export and measurements require Playwright/Chromium. `PLAYWRIGHT_CORE` can point to an
external module installation; `AGENTGATE_BROWSER_EXECUTABLE` selects the checker browser executable.

```sh
node bin/figma.mjs frames 'https://www.figma.com/design/FILE/Name?node-id=1-2' --transport api
node bin/figma.mjs pull 'https://www.figma.com/design/FILE/Name?node-id=1-2' --transport api --out screen/package
node bin/check.mjs screen/package --url http://127.0.0.1:5173 --root main --out report
```

For the compatibility browser adapter, run `figma login` and choose `--transport browser`. It uses Figma's
unpublished scene graph, so UI changes can break the adapter. API permission/rate errors are explicit; no
hidden transport fallback occurs. Tokens never enter exported packages or image CDN requests.

## Modules

| Module | Responsibility |
|---|---|
| `cli.mjs` | Strict shared arguments, URL and numeric validation |
| `figma-target.mjs` | Figma URL/node parsing and transport selection |
| `figma-api.mjs` | Documented REST endpoints, version-pinned bounded rendering |
| `figma-page.mjs` | Browser scene-graph compatibility adapter |
| `package.mjs`, `files.mjs` | Common package format, inventory verification and atomic publication |
| `spec.mjs`, `outline.mjs` | Normalized measurement data and concise worker brief |
| `page-read.mjs`, `compare.mjs` | Browser measurements, image differences and matching |
| `stand.mjs` | Configuration, readiness and ownership of stand/fixture processes |
| `fixtures.mjs` | Loopback GraphQL replay/record, canonical variable identities |
| `browser.mjs` | Playwright discovery, Chromium and private Figma profile |

A package contains `brief.md`, `frame.png`, `data/nodes.json`, `data/spec.json` and `manifest.json`; optional
context images and assets join the same SHA-256 inventory. Missing files or changed bytes are errors.
Exports preserve the previous package when generation fails. Failed and concurrent exports cannot publish
a partial package over it.

Measurement output includes cropped/whole-page screenshots, pixel difference and structured findings.
Exit 0 means measured properties passed, exit 1 means differences, and exit 2 means verification could not
run. Independent visual and behavioral review remain necessary. Baselines waive only identical initial
discrepancies under the exact same measurement contract.

Fixtures are keyed by operation and canonical variables. Only a deliberately supplied plain operation file
is a generic fallback. Missing data is a visible GraphQL error unless `--allow-missing` is explicitly used.
An existing unhealthy stand is an error; cleanup stops only processes owned by this invocation. `--keep`
prints ownership information and intentionally leaves processes alive.

## Tests

```sh
npm run test:unit       # no browser/account/network required
npm run test:browser    # actual Chromium workflows; requires a working browser environment
npm test               # both suites
```

Unit tests cover parser behavior, fixture safety/replay, atomic publication, Figma API protocol and stand
process lifecycle. Browser tests exercise real measurement/baseline/responsive/cancellation scenarios and
modern CSS color conversion. The release's actual results and environment limits are recorded in
[validation](../docs/validation.md).

See the complete [design guide](../docs/design.md) / [руководство](../docs/ru/design.md) for screen and project
configuration, exceptions, transport selection and integration with the gate.
