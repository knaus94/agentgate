# Leading from Codex, and Codex only

[Русский](ru/codex.md)

`agentgate` can be led from a Claude Code session (`/dev`), from a Codex session (`$dev`), or from either.
The gate, the stages, the checks, the panel, isolated tasks, accounts and extensions are the same: none of
them depends on which CLI the lead is. What differs is how the lead command is installed and, in the
Codex-only setup, which models do the work.

## Choose at install

```sh
agentgate install --lead claude   # /dev in Claude Code; Claude and Codex models work (the default table)
agentgate install --lead codex    # $dev in Codex; every worker is a Codex model
agentgate install --lead both     # /dev and $dev; the default table
```

Without `--lead`, the first install asks when a terminal is attached; `scripts/install.sh` asks the same
question, or takes `AGENTGATE_LEAD`. The choice is remembered, so `agentgate update` repeats it silently.
Choosing a lead explicitly removes the other one's install; `--lead both` keeps both.

For Codex the installer writes three things:

| What | Where | Why |
|---|---|---|
| the `dev` skill | `~/.agents/skills/dev/` | `$dev <task>` in a session; it is never started from keywords |
| a rule | `~/.codex/rules/agentgate.rules` | lets Codex run `agentgate` outside Codex's own sandbox (see below) |
| a Stop hook | `~/.codex/hooks.json` | keeps the lead from ending a turn on an unfinished task |

Codex does not run a hook until you have reviewed it: open a Codex session once and trust the agentgate hook
with `/hooks`. Everything else works without that step; the hook is a safety net.

## Why the rule

`agentgate` runs every worker in its own sandbox (`bwrap`), talks to the model providers and keeps its state
under `~/.local/state/agentgate`. Inside Codex's sandbox none of that is possible: a sandbox cannot be started
within it, the network is off and the home directory is read-only. So the lead has to run `agentgate` outside
its sandbox, and the rule allows exactly that command, nothing else:

```
prefix_rule(pattern=["agentgate"], decision="allow")
```

The workers stay in agentgate's sandbox, so writers and reviewers are confined as before. The rule matches
only a plain command — `agentgate <action> --file <path>` — and not one with a pipe, a heredoc or `&&`. The
lead is told this, writes its inputs to files first, and when it slips, `agentgate` answers with what to do
instead of failing somewhere deep.

## The Codex-only table

`agentgate install --lead codex` selects the built-in `codex` profile (`agentgate config profile codex`):

| | light | medium | heavy | ultra |
|---|---|---|---|---|
| Context | the lead's own | scout `terra:medium` | the accepted design | the accepted design |
| Architect | the lead | the lead | `astra:high` ∥ `sol:xhigh` | `astra:max` ∥ `sol:max`, then cross-critique |
| Writers | `sol:medium` | `astra:medium` | `astra:high` | `astra:high` |
| Review: architecture + simplicity | the lead | `sol:high` | `sol:xhigh` | `sol:xhigh` |
| Review: correctness | the lead | `sol:high` | `sol:xhigh` | `sol:max` ∥ `terra:max` |
| Review: security | — | — | — | `sol:xhigh` |
| Effort ceiling | `high` | `high` | `max` | `max` |

Other roles: consultations on `sol:medium`, UI design on `astra:medium`, the browser pass on `terra:high`.

### Why these choices

Public data, November 2026 (Artificial Analysis Intelligence Index v5.0 as reported by Respan; OpenAI's own
guidance as reported by DataCamp):

| Effort | Astra | Sol | Terra | Luna |
|---|---|---|---|---|
| max | 57 · $6.24 | 55 · $1.27 | 51 · $0.96 | 28 · $0.15 |
| xhigh | 55 · $3.66 | 52 · $0.76 | 45 · $0.40 | 23 · $0.10 |
| high | 55 · $3.24 | 50 · $0.59 | 42 · $0.26 | 21 · $0.07 |
| medium | 51 · $2.73 | 46 · $0.36 | 38 · $0.17 | 18 · $0.04 |

(index score · cost per task)

- **Astra creates, Sol verifies.** Astra is the strongest of the four at every effort, and Sol is close behind
  for a fifth of the cost. With two strong models, "the author never reviews its own code" is kept by exactly
  this pair. OpenAI itself recommends Sol or Astra for the hardest coding.
- **Astra is never set to `xhigh`.** On the index it scores the same 55 at `high` and at `xhigh`, for more
  money and twice the wait. The step that buys something is `max`, so that is where the architects of ultra
  run and where a stalled correction escalates.
- **Sol's effort is where its quality comes from**: 46 at medium, 50 at high, 55 at max. A reviewer reads
  rather than writes, so the cost of a high effort is small; reviewers run at `high` and above.
- **Terra reads.** It is the fastest of the usable models (91–109 tokens per second) and cheap, which is what
  a scout needs, and OpenAI positions it for computer-use work, which is the browser pass. At `max` it reaches
  51, so in ultra it is a second, different pair of eyes on correctness.
- **Luna is not used.** At 28 it is half the others.

### What is weaker than the default table

- **One vendor.** In the default table code written by one vendor is reviewed by another, and models of
  different families fail in different ways. Here the reviewer is a different model of the same family.
- **One pool of limits.** Every lane draws on the Codex subscription. Several Codex accounts
  ([accounts](accounts.md)) spread it.
- **No independent data on code review.** As with the default table, reviewer choices are inferred from
  coding and reasoning results.

A profile is only a layer under your settings: `agentgate config set modes.medium.backend sol:high` still
overrides one cell, and a file in `~/.config/agentgate/profiles/<name>.json` is a profile of your own
(`agentgate config profiles` lists them).

## Sources

- [Respan: GPT-6.1 Sol vs Terra vs Luna vs Astra](https://www.respan.ai/articles/gpt-6-sol-vs-luna-vs-astra)
- [DataCamp: GPT-6.1 Terra](https://www.datacamp.com/blog/gpt-6-1-terra)
- [OpenAI Codex: hooks](https://learn.chatgpt.com/docs/hooks) and [skills](https://learn.chatgpt.com/docs/build-skills)
