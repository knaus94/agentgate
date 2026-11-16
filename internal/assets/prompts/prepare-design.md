## A design is part of this task

The packet holds `design_catalogues` (a bounded listing of the linked Figma file, collected by the host tool),
`design_settings`, and, for a running task, `design_context`: the selected packages and the host's last
observation. You do not pull or parse Figma data, start a stand or a browser, or measure anything.

In the plan a design is `{figma, name, url, root, stream, viewport, device, steps, compare}`: the link to the
selected content node, a short screen name, the local page that shows it, the selector of the element that
implements it, the stream that builds it, and how the page reaches the state the node shows. The tool imports the node when that stream's stage opens, measures the page before
the work and declares the layout check itself: do not put layout commands or baseline paths in `checks`.

- **Select content, not catalogues.** A section or a page is a catalogue, not an exportable screen. A reused app
  shell is outside a page-content task: select the content node instead of ignoring most of a full-screen
  design. The package keeps the parent screen for placement review, so the gap to the header stays in
  acceptance.
- **Pair equivalent boxes, including padding.** A 390 px content frame with 12 px side padding matches a 390 px
  wrapper with that padding, not its 366 px child. A new page's root may not exist yet; never replace the
  correct root with the shell to get a measurement before the work.
- **Viewport and device are separate.** A phone or tablet reference uses `device: "mobile"` with its own CSS
  viewport; a narrow desktop window is still desktop, with a scrollbar gutter and no touch.
- **Keep the requested fidelity.** "No overflow" and a matching column count do not replace "matches the
  design". Keep shell-to-content spacing, artwork and crop, and control semantics in the requirements even when
  the shell is outside the write scope.
- **A responsive range needs a bounded matrix.** Inspect the breakpoints and propose reference widths plus the
  relevant boundaries; say what each row proves. If only reference widths are authorized, record that limit and
  do not claim the range.
- **A state a URL does not reach needs steps.** Designs of one page that differ by state — a dialog, a tab, a
  filled form, an error — open the same URL. Give every one of them but the page as it opens `steps`, run before
  anything is measured: `{"click": "<css>"}`, `{"fill": "<css>", "value": "…"}`, `{"wait": "<css>"}`, with
  selectors the implementation will have (`data-*` attributes the stream adds are fine; say so in its brief).
  When a state cannot be reached by steps — it needs data the stand does not have, or a timing — set
  `compare: "review"`: the reviewer judges it from the pictures and nothing is measured. The validator refuses
  two entries that would measure one page in one state.
- **Behaviour comes from the task and the repository, not from a still image.** Missing API data does not
  change agreed behaviour; a field-shaped rectangle is not proof of editability. Raise a material ambiguity as
  a question.
- **Overlapping artwork** is told apart with `map.select` and a real, unique CSS selector inside the root.
  Selection names a layer; it does not exempt its position, size or styling. Report the proposed mapping in
  `setup`; do not edit a map, an ignore list or private state.
- **A missing baseline is a strict comparison, not a prerequisite.** Never ask for one to be created from
  written code. The host's last observation may predate the present code: it is for diagnosis.
- `setup` lists the exact stand commands and configuration to authorize. Do not overwrite an existing
  `design.stand` configuration. A page that needs more than the front end — a backend, a database with its
  data, a signed-in user — needs `design.stand.services` (each `{command, ready}`, started before the front end
  and stopped after the check) and `design.stand.session` (`{command}` that prints the cookies of a signed-in
  user as JSON). Propose them with the exact commands; the tool opens every design's page before the writers
  start, so a missing session is found then, not after the work.
- **The catalogue is bounded.** When a node you need is deeper than `design_catalogues` lists it, or in another
  part of the file, return `BLOCKED` with `design_requests: [{node, depth}]` — the node id (`8669:28386`) or its
  own link, and how many levels to list, up to 6 — and empty `questions`. The tool reads them and gives them back
  to you in this same conversation, up to three times per call. Never ask the lead to read Figma for you.
- A technical block — the stand cannot be described, a node cannot be read — is `BLOCKED` with a concrete
  `summary` and `setup`; `questions` may then be empty.

A design belongs to one stage through its stream. In a larger task, plan the data and behaviour in earlier
stages and the screen in the stage whose stream builds it.
