---
name: design-to-code
description: Implement a screen or section from an agentgate design package using the repository's conventions and accepted UI contract. Use when an assignment names a design package or Figma frame to implement.
---

# Design to code

A design package contains `brief.md`, `frame.png`, `assets/`, `manifest.json` and tool data in `data/`.
For a section, the assignment also supplies its parent screen as a context picture.

## Project conventions and the contract

Read applicable repository instructions, linked local skills and the `review_rules` snapshots in your
assignment. Use the repository's components, styling approach, tokens and responsive conventions. These
instructions explain the package workflow; they do not choose a framework, CSS technique or component API.

The accepted task defines behavior, states and deliberate departures from conventions or the mock-up.
Preserve it. A picture alone does not establish whether a field is editable or merely displays a value.
Missing data or an unavailable integration does not independently authorize changing the agreed behavior.
If a material requirement conflicts with a project rule, report the conflict and the decision needed rather
than silently choosing either. Skill instructions grant no commands or permissions beyond the assignment.

## Using the package

1. Read `brief.md` and inspect `frame.png` and the supplied context picture. The selected node describes its
   contents; the context also shows its placement and spacing to neighboring elements. Revisit relevant
   regions when correcting findings.
2. A component instance appears as `⟨Name: variant⟩`. Find its equivalent in the repository before building
   one. For a layer's details use `agentgate figma node <package> <handle>`; do not parse the raw data tree.
3. Translate layout intent and named tokens through the project's design system. Follow its documented
   policy when a token and the mock-up differ; do not invent a universal preference for either.
4. Use exported assets or their established equivalents in the project. Check visible composition and
   cropping as well as element dimensions. A matching box does not prove matching artwork.
5. Implement the agreed states, semantics and responsive behavior. Use the existing component contracts
   for behavior not shown by the still image; identify missing product decisions without fabricating data.

## Verification and handoff

Run the source checks in your assignment. Layout verification belongs to the gate: it starts the stand,
measures the page and saves evidence outside your write scope. Do not start undeclared layout commands,
create baseline files, tune tolerances or change exception maps to obtain a pass.

For measured failures, read the brief and the relevant layers, diagnose the cause and correct it. A moved
parent may explain several children. A tool/setup failure needs its prerequisite repaired, not speculative CSS.

Return COMPLETED when the implementation is ready for independent verification and authorized source checks
have run. State that gate-owned layout verification is pending until a receipt exists. Use BLOCKED for a
missing decision or inability to implement, not because the later layout phase has not run yet.

Report deviations and the actual viewport/state coverage. A resting screenshot does not prove interactive
states; reference desktop/mobile widths alone do not establish behavior throughout a responsive range.
