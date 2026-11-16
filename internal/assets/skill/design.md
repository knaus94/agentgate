# A Figma design

`agentgate prepare` with the user's request. The host tool reads a bounded catalogue of the linked file; the
preparation worker finds the repository's conventions, selects desktop and mobile content nodes and pairs them
with implementation roots. You get a brief, product questions and the exact setup and check commands to
authorize. You do not extract layers, compare screenshots, tune CSS or write measurement scripts.

## Reaching Figma

`agentgate figma status` says who is signed in and whether the session still works; `agentgate figma login`
signs in once, in a browser window the user completes. A REST token in `FIGMA_ACCESS_TOKEN` works without a
browser. The preparation reads the catalogue itself; you never call the exporter's scripts or paste their
output into `notes`.

## In the plan

```json
{"design": [{"figma": "https://www.figma.com/design/<key>/<file>?node-id=12-345", "url": "/checkout",
             "root": "main .checkout", "name": "checkout", "stream": "web",
             "device": "mobile", "viewport": "390x844"}]}
```

`figma` is the link to the frame, `url` the page that shows it, `root` the element that implements it,
`stream` the stream that builds it. A screen that exists already is named instead: `{"screen": "<dir>",
"stream": …}`. The default device is desktop whatever the width; a phone or tablet reference uses
`device: "mobile"` with its own viewport. Pair the exported frame with an equivalent root including its
padding. `map.select` accepts unique CSS selectors and does not exempt a selected element from comparison.

A design belongs to the stage of its stream, so a staged plan can do the backend first and the screen in a
later stage; that stage gets the layout check.

How the front end starts is said once per repository, in `.agentgate.json`:

```json
{"design": {"screens": "apps/web/testing/design",
            "stand": {"command": ["yarn", "dev"], "ready": "http://localhost:4200",
                      "env_file": ".env.local", "graphql_env": ["VITE_GRAPHQL_URL"],
                      "cookies": [{"name": "session", "value": "…"}]}}}
```

## What runs

When the stage of the design opens, the tool imports the selected nodes, prepares the design packages and
records the page as it is before the work; a page or a root that does not exist yet is allowed. Writers read
their package and implement the code; they run only their declared source checks. The tool owns the layout
commands, the browser and stand lifecycle, the baseline files and the output paths. Verification measures,
then an independent reviewer judges the pictures and the behaviour.

A layout PASS covers measured properties only. Built-in blocking rules make the reviewer assess artwork and
crop, placement in the shell, control semantics, and coverage and exceptions; missing evidence keeps the stage
open. Report the tested widths and states and the accepted differences precisely: two endpoint screenshots do
not establish responsive behaviour between them.

## When something is wrong

- A tool could not observe the page (`needs: setup`): the page did not open, or it ended at another path (a
  redirect to a login: the stand needs a session or data). Repair the stand; it is not a CSS defect for a
  writer. A page that opened where it was asked to and does not show the design's element is a defect of the
  code: it goes to the writer with the `data-*` attributes the page does carry.
- A design that cannot be imported (`needs: setup`) names the exporter's own error. An icon Figma does not
  render is listed in the package as missing and does not stop the import. A screen the tool makes is kept
  under `design.screens` (default `.agentgate/design`), never among the task's files.
- The measurement contract is wrong: `agentgate prepare` with `notes` revises the plan with the task's
  observations; apply it with `amend`. Code already written is never taken as the page's before-image.
- Layout differences go back to the writers by themselves. A product exception needs the user's decision and
  a reason. Do not broaden tolerances, replace a content root with the shell, or ignore most of the design to
  pass a check.
- `refresh_designs: true` in `prepare` re-reads Figma when the selected design itself changed.
