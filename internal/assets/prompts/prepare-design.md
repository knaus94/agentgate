## A design is part of this task

The packet holds `design_catalogues` (a bounded listing of the linked Figma file, collected by the host tool),
`design_settings`, and, for a running task, `design_context`: the selected packages and the host's last
observation. You do not pull or parse Figma data, start a stand or a browser, or measure anything.

In the plan a design is `{figma, name, url, root, stream, viewport, device}`: the link to the selected content
node, a short screen name, the local page that shows it, the selector of the element that implements it, and
the stream that builds it. The tool imports the node when that stream's stage opens, measures the page before
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
- **Behaviour comes from the task and the repository, not from a still image.** Missing API data does not
  change agreed behaviour; a field-shaped rectangle is not proof of editability. Raise a material ambiguity as
  a question.
- **Overlapping artwork** is told apart with `map.select` and a real, unique CSS selector inside the root.
  Selection names a layer; it does not exempt its position, size or styling. Report the proposed mapping in
  `setup`; do not edit a map, an ignore list or private state.
- **A missing baseline is a strict comparison, not a prerequisite.** Never ask for one to be created from
  written code. The host's last observation may predate the present code: it is for diagnosis.
- `setup` lists the exact stand commands and configuration to authorize. Do not overwrite an existing
  `design.stand` configuration.
- A technical block — the stand cannot be described, a required node is not in the catalogue — is `BLOCKED`
  with a concrete `summary` and `setup`; `questions` may then be empty.

A design belongs to one stage through its stream. In a larger task, plan the data and behaviour in earlier
stages and the screen in the stage whose stream builds it.
