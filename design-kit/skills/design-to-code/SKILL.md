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

Check as you go, a section at a time, not once at the end. Measure again after every fix; if eight rounds
have not cleared a difference, stop and report it rather than keep adjusting.


Run the check the task declares, of the form `agentgate layout <package> --url <page> --root <css>`. It prints at most
twenty lines, one per difference, most important first:

    3 ‹84b1› left-payment-method-panel → div.panel: off by x +8 y 0

- `‹84b1›` is the layer in the brief, the selector is the element it was matched to.
- Fix causes, top down: one moved parent explains its children, and they are not listed.
- `not on the page` for data the stand does not have is not yours to fix: report it.
- `font X → drawn with Y` means the font is not loaded on that element, usually a missing `font-family`.
- Look at `marked.png` only when a line cannot be understood from its numbers.

- `<button>`, `<input>` and `<select>` do not inherit the font: set it on them, or the text is drawn with the
  browser's own.
- The check may already report differences before you start; the task says how many. Leave no more than that.

Before you report, look at `frame.png` and at the page once, side by side: the check judges numbers, and a
wrong icon or a missing gradient has none.

The check measures the screen at rest. A hover, a pressed or a disabled look it has not seen is not proven by
its PASS: say in your report which states you built and what each was taken from.

Do not change the package, its map file or the tolerances to make the check pass.
