# design-kit

Two tools that let a coding agent implement a Figma design and prove the result, with as few model tokens as
the work allows: what can be computed is computed, and a model reads only short results.

    figma login                              sign in once, in a browser window
    figma frames <figma-url>                 the frames of a page: choose what to pull
    figma pull <figma-url> --out <dir>       write a design package
    figma node <dir> <handle>                one layer in full, on request
    check <dir> --url <page> --root <css>    compare a page with a package; exit 1 when it differs

Needs Node 20+ and `playwright-core` with a Chromium (the browser tooling of agentgate is enough).

## The package

| File | Read by | Holds |
|---|---|---|
| `brief.md` | workers | the layers as intent: layout rules, type, colour, tokens. About 2 thousand tokens for a page section |
| `frame.png` | workers | the picture of the node |
| `assets/` | workers | icons as SVG, pictures as PNG |
| `manifest.json` | the lead | source, file version and a digest of the content, to pin in a plan |
| `data/` | tools only | the node tree in Figma's REST format and its normalised form |

A component instance is one line of the brief, repeated siblings are one item of a list, and positions that
follow from auto layout are not printed. `figma pull` reports how many tokens the brief costs a reader: pull a
narrower node when it is too many.

## The check

`check` opens the page, reads every text and painted box with its geometry and computed style, and matches
them to the design without any markup in the code: a text by its words, a box by where it lies. It reports
position (against the matched parent, so a moved panel is one line), size, font, colour, radius and what is
absent. Run it as a declared check of the task; the stand it opens is the project's business.

A map file names what must not be judged: `{"ignore": {"<handle>": "why"}, "dynamic": ["<handle>"]}`.

## The Figma session

There is no token and no cookie file. `figma login` opens a window; the session stays in a browser profile
under `~/.local/share/agentgate/private/figma`, which agentgate hides from its workers.

`figma pull` works with view access. It reads the document from the Figma web application's own scene graph,
because the REST API and the MCP server are rate-limited to a few calls a month for a view seat. That graph is
not a published interface: when Figma changes it the tool stops with a message saying which part is gone. A
Dev seat in the file's team makes the official API and MCP usable instead.
