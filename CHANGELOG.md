# Changelog

[Русский](CHANGELOG.ru.md)

## Unreleased

- Acceptance requirements now carry per-criterion review evidence for single-pass and staged tasks. The gate
  validates code locations, current successful check receipts and observed browser scenarios; partial or missing
  proof prevents completion. Check kinds distinguish tests, types, lint, builds, measurements and runtime checks.
- Extensions can add scoped `review_rules` with mandatory or advisory results, assessed by an existing reviewer.
  Instruction changes and new evidence invalidate the assessment; unavailable mandatory rules fail closed.
  `review {recheck: true}` requests a new judgement without bypassing acceptance. Existing plans without these
  criteria keep their behavior; plans with requirements need a structured review before completing.
- Simplicity guidance judges an abstraction's concrete benefit, not just its caller count, and distinguishes
  legitimate compatibility recovery from fallbacks that hide a broken primary contract.

## 0.9.11

- A task continued on a gate that was opened before its design could be measured no longer fails its layout
  check for a missing record: with no record, every difference counts. `agentgate layout --help` is not an error.

## 0.9.10

- **A page that does not exist yet can be designed.** Measuring before the work no longer fails when the page
  or its root element is not there, and a page that shows next to nothing of the design is taken as not built:
  nothing it lacks counts as known.
- A screen that has its description but no design is completed from its `figma` link at launch.
- A link to a Figma page or section, not a frame, is answered with the frames to choose from; two Figma
  commands at once wait for each other instead of failing; `agentgate figma --help` is not an error.
