# Design

[Русский](ru/design.md)

A stream can implement a Figma design, and the gate can tell whether it did. What can be computed is computed
by tools; a model reads short results.

## The flow

| Step | Who | What happens |
|---|---|---|
| Sign in | you, once | `agentgate figma login` opens a browser window. The session stays in a profile under `~/.local/share/agentgate/private`, which no worker sees. No token and no cookie file |
| Choose | the lead | `agentgate figma frames <url>` lists the frames of a page |
| Pull | the lead | `agentgate figma pull <url> --node <id> --out <dir>` writes a design package |
| Plan | the lead | `design [{figma, url, root, stream}]` in the plan: the design is pulled at launch |
| Measure | the gate | before anyone writes, the page is compared with the package: what differs already is not the task's |
| Write | the stream's writer | reads the brief, about two thousand tokens for a page section, and runs the layout check itself |
| Check | the gate | the layout check again, as a declared check. A new difference blocks the review |
| Judge | the first reviewer | is shown the design next to the page and looks for what numbers do not carry |

## The package

| File | Read by | Holds |
|---|---|---|
| `brief.md` | the writer, the reviewer | the layers as intent: layout rules, type, colour, design tokens |
| `frame.png` | the writer, the reviewer | the picture of the node |
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
