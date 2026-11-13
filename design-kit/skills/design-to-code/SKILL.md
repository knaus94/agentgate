---
name: design-to-code
description: Implement a screen or a section from a design package written by `agentgate figma pull`, and prove the result with `check`. Use when a task names a design package, a Figma frame, "по макету", "сверстать", or asks to match a mock-up.
---

# Design to code

A design package is a directory: `brief.md`, `frame.png`, `assets/`, `manifest.json` and `data/`.
`data/` is for tools. Never read it: it is hundreds of kilobytes and tells you nothing the brief does not.

## Writing

1. Read `brief.md` once and look at `frame.png` once. That is the whole design.
2. `⟨Name: variant⟩` is a component instance. Find the project's component of that name and use it with the
   matching props. Build one only when the project has none; then ask for its inside:
   `agentgate figma node <package> <handle>`. Ask for one layer at a time, not for the tree.
3. A value with a token in parentheses — `#1E1F23 (neutral/800)`, `gap 8 (spacing/8)` — is written with the
   project's token of that name, not as a literal. A value without a token is a literal.
4. Layout is the rule in the brief, not coordinates: `row gap 16 pad 32`, `w:fill`. Positions are printed only
   for layers the design places absolutely.
5. Icons and pictures come from `assets/`, unless the project already has the same icon. Add no icon package
   and draw no placeholder. An icon whose colour follows its text or its state is inline SVG with
   `currentColor`, not an `<img>`: an image cannot be recoloured.
6. The brief shows one state with sample data. Words and numbers that come from data stay data. States the
   brief does not show (loading, empty, error, disabled) follow the project's existing screens; say in
   `deviations` which ones you added and from where.

7. When a project token and the design disagree by a pixel or a shade, the token wins: consistency with the
   product matters more than the mock-up's exact number. Say so in `deviations`. A larger gap is a question.
8. No inline styles, except a value that is computed at run time.
9. A section fills the width it is given. The page that places it sets its maximum width and outer padding;
   the section does not carry them.

## What the picture does not show

A mock-up is one still frame. These are yours to get right, and no number in the check covers them:

- Elements by meaning: `<button>` for an action, `<a>` for navigation, never a `<div>` with a click handler;
  headings in order; a `<label>` or `aria-label` on every control, `aria-label` on an icon-only button,
  `alt` on a picture and `aria-hidden` on a decorative icon.
- Focus: every interactive element shows `:focus-visible`; never remove an outline without a replacement.
- States: hover, active, focus and disabled. Take them from the component's variants in the brief when it
  lists them, otherwise from the project's same control; do not invent a look.
- Content that is not the mock-up's: a long name, an empty list, a large number. Text truncates or wraps where
  the design gives it one line, and a flex child that truncates needs `min-width: 0`.
- Pictures carry `width` and `height`, so the page does not jump while they load.
- Motion: name the properties in a transition, never `transition: all`.

## Proving

Run the source checks in your assignment. Layout verification belongs to the gate: it starts the stand,
measures the page and saves its evidence outside your write scope. Do not run an undeclared layout command,
create baseline files, tune tolerances, edit maps or diagnose the gate's private state.

When a correction includes measured layout failures:

- Read the brief and the relevant reported layers. Fix the cause top down; a moved parent explains children.
- Use the assigned package's node command for a specific layer when needed, not its raw data tree.
- Missing business data or an ambiguous product state is a question, not permission to fabricate values.
- A tool/setup failure is not a request to adjust CSS. Report the concrete prerequisite instead.
- `<button>`, `<input>` and `<select>` need an explicit/inherited font or they use the browser's own.

Return COMPLETED when your implementation is ready for independent verification and its authorized source
checks have run; say layout is pending, never that it passed without a gate receipt. Use BLOCKED for a real
missing decision or inability to implement, not because the gate-owned layout phase has not run yet.
The reviewer checks the design against the page. A check of the resting state does not prove hover, focus,
pressed or disabled states; identify which states you built and which existing component defines them.
