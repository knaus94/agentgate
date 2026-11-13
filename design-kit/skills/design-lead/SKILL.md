---
name: design-lead
description: Coordinate a Figma implementation through agentgate preparation, writers and independent review. The lead owns product scope and authorization, not design extraction or CSS diagnosis.
---

# Leading design work

1. Pass the original request to `agentgate prepare --file <request JSON>`. The host tool reads Figma's catalogue;
   a read-only worker discovers the repository and drafts a saved plan. Do not repeat its searches, export
   frames, parse data/, inspect screenshots or write implementation guidance yourself.
2. Read the returned summary and brief for product scope, missing behavior decisions and resource needs.
   Resolve those decisions with the user and return them in prepare.notes. Approve the exact stand/check
   commands under the repository's policy. A prepared plan is not permission to run its checks.
   Phone/tablet entries need an explicit mobile device mode and viewport. A technical BLOCKED result with
   no user questions is still a valid report: use its setup prerequisites, not private worker files. An
   absent baseline is a strict check, never a prerequisite to reconstruct after implementation.
3. Launch the saved plan. The gate imports selected content nodes and handles the original measurement;
   writers implement using their packages and existing components. A new page may have no root yet.
4. Run review. The gate measures and reviewers inspect code and pictures. Forward code failures with `fix`;
   writers diagnose and correct them in their existing sessions. Do not take over their work.
5. For a setup/measurement-contract failure, ask prepare to revise the plan using the recorded failure, then
   apply the approved correction with `replan`. Original snapshots and findings stay intact. A held task
   must be explicitly resumed; accepted stages stay frozen. Never alter state or rebaseline completed code.
6. Triage the reviewers' evidence and request product decisions for justified exceptions. Do not loosen
   tolerances or replace a content root with the app shell to pass. Finish only from the gate's receipts.
