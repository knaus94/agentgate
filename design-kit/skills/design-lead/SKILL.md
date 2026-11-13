---
name: design-lead
description: Prepare a task that implements or changes UI from a Figma design — choose the frames, pull a design package, put it into the plan and declare the check. Use as the lead when a request names a Figma link, a mock-up, "по макету", or asks to match a design.
---

# Leading design work

You prepare; tools measure; workers read one short brief. Never open Figma for a worker and never paste design
data into a brief: point at the package.

## Before the plan

1. `figma frames <figma-url>` lists the frames of the page. A screen is usually several: one per state
   (empty, filled, error) and per width. Decide which the task covers and say which it does not.
2. `figma pull <figma-url> --node <id> --out <dir>` once per frame. Pull the node the task changes, not the
   screen around it: the header and the footer of an app shell are half of a frame and none of the task. The
   command prints what the brief costs a reader in tokens; above about three thousand, pull a narrower node or
   split the work into streams with a package each.
3. Read `brief.md` yourself. What it cannot tell is yours to settle in the plan's brief, in words:
   - behaviour: what each control does, validation, requests, what changes on screen after them;
   - states the design does not draw, and which existing screen each follows;
   - widths between the drawn ones — no design says how the screen looks there, the project's rules do;
   - where the product deliberately differs from the mock-up.
4. What the product shows differently goes into a map file, each entry with its reason
   (`{"ignore": {"<handle>": "why"}, "dynamic": ["<handle>"]}`). A map is yours: a writer may not edit it.
   A screen whose map ignores most of the design is checked against too little — say so to the user.

## In the plan

- `context`: the package's `brief.md` and the worker skill. Nothing from `data/`.
- `ui`: a local contract `{summary, criteria, preserve}` that names the package; no design pass is needed when
  the design exists.
- `checks`: the `check` command with `inputs` covering the package and the changed files, so its receipt goes
  stale when either changes. State the number of differences the screen has now: the task must not raise it.
- The stand is running before `launch`, started by you with the user's approval of the commands. A check that
  cannot open the page fails for everyone.
- Browser verification by a model is for behaviour scenarios only. Layout is the check's job.

## After the writers

Read the check's summary, not the pictures. A difference a writer could not remove is either a decision for
you (accept it into the map with a reason) or a finding to send back; it is never left unexplained.
