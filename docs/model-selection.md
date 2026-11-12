# Why these models

[Русский](ru/model-selection.md)

The default mode table is built from public data, not from taste: independent benchmark runs, the vendors'
own published guidance, and list prices. This page shows the data, the decisions it led to, and where the
data is thin. Snapshot: **November 2026**. Models change; re-check before you trust it, and
[override anything](configuration.md) that no longer holds.

## The data

### Independent runs (Vals AI)

Same harness for every model. Vals does not state the reasoning effort it used, so treat cross-vendor gaps as
"each model as shipped", not as an equal-effort comparison.

| Benchmark | Opus 5.5 | Sonnet 5.5 | GPT-6 Astra | GPT-6.1 Sol |
|---|---|---|---|---|
| SWE-bench Multimodal (real repositories, front-end heavy) | **70.2%** | 58.3% | 43.3% | 43.5% |
| Terminal-Bench 4.0 (agentic terminal work) | **65.2%** | 64.1% | 59.6% | 55.1% |
| Code Migration v1.1 | 81.0% | **86.9%** | 80.6% | 77.3% |
| ProgramBench | **18.5%** | 6.5% | 5.5% | 3.0% |
| Vibe Code Bench v1.1 (building an app) | 90.3% | **92.4%** | 89.6% | 88.9% |
| IOI (algorithms) | 95.1% | 83.1% | **100%** | 96.9% |
| SRE Bench (operations) | 33.6% | 30.2% | **56.9%** | 50.8% |
| CyberBench v1.1 | **55.4%** | — | 41.1% | — |

Cost and time per benchmark task, same runs:

| | Opus 5.5 | Sonnet 5.5 | GPT-6 Astra | GPT-6.1 Sol |
|---|---|---|---|---|
| SWE-bench Multimodal | $3.63 · 18 min | $3.26 · 24 min | $1.81 · 7 min | **$0.35 · 7 min** |
| Terminal-Bench 4.0 | $12.04 · 64 min | $10.75 · 82 min | $9.58 · 36 min | **$1.72 · 45 min** |
| Code Migration v1.1 | $72.40 · 3 h 29 min | $43.88 · 4 h 07 min | $62.07 · 1 h 27 min | **$9.51 · 1 h 39 min** |

### Vendor-reported (Anthropic's Fable 5.2 launch table)

Measured at each model's highest available effort unless noted. Not independently replicated.

| Benchmark | Fable 5.2 | Opus 5.5 | GPT-6 Astra |
|---|---|---|---|
| FrontierCode 2.0 (would a maintainer merge it) | 49.1% at xhigh, 45.3% at high | 45.5% | 42.8% |
| FrontierSWE v2 | 69.9% | 62.3% | 65.5% |
| Terminal-Bench-Science 0.1 | 74.6% | 58.7% | 68.1% |
| Remote Labor Index (open-ended projects) | 41.3% | 21.3% | 20.8% |
| OSWorld 2.1 (computer use) | 89.3% | 87.2% | 79.5% |

Artificial Analysis, as reported by secondary coverage: on Terminal-Bench 4.0 Fable 5.2 ties Opus 5.5 at 60%
while Sonnet 5.5 reaches 64%; Fable 5.2 at high or xhigh uses about 98,000 output tokens per task against
173,000 for Opus 5.5 at max.

### List prices, per million tokens

| | Input | Output |
|---|---|---|
| Claude Fable 5.2 | $8 | $40 |
| Claude Opus 5.5 | $4 | $20 |
| Claude Sonnet 5.5 | $2 | $10 |
| GPT-6 Astra | $10 | $50 |
| GPT-6.1 Sol | $2 | $10 |

The Fable 5.2 price comes from one secondary source; Anthropic's reference lists $10 / $50 for Fable 5.1.
On a subscription you pay in usage limits rather than dollars, but the ratios still tell you what is heavy.

### Effort

- Anthropic: `low` for simple tasks and sub-agents, at least `high` for intelligence-sensitive work, `xhigh`
  for coding and agentic work where it counts, `max` only when measurement shows headroom. Opus 5.5 defaults
  to `medium`; for Sonnet 5.5 start at `medium` for agentic coding. Lower effort on the newest models often
  matches the previous generation at high effort.
- OpenAI ships Sol with `low` as its default level and Astra with `medium`, and describes Sol as "near-Astra
  performance at a lower cost".
- Returns flatten near the top: one published sweep (previous-generation Sol) scored 53.6 at low, 60.6 at
  medium, 64.1 at high and 65.1 at xhigh. A small observational study found xhigh mainly buys *first-try
  reliability* rather than peak score.

## What follows from it

One rule organises the table: **the strongest model the mode can afford creates; the other frontier models
verify.** A defect that is never written costs nothing to find, so the best model goes to design and code, and
the review is done by models that did not write it.

**1. Fable creates in heavy and ultra.** It is the strongest coder in the published data (49.1% against 45.5%
on FrontierCode 2.0, 69.9% against 62.3% on FrontierSWE v2), and its margin is largest on open-ended
multi-step work. At `high` it already equals Opus at its maximum for about half the tokens, so heavy runs it
at `high`; the margin over Opus opens at `xhigh`, which is where ultra runs it.

**2. Opus writes in medium.** It is the best coder in the independent runs (70.2% on real repositories, 12
points above Sonnet, 27 above both OpenAI models) at about the same cost per task as Sonnet. Everyday work is
most of the work, so this is where an independent measurement matters most.

**3. Front-end follows the same models.** SWE-bench Multimodal is the closest public proxy for front-end work,
and the Claude models lead both OpenAI models there by 15–27 points. Astra is not a front-end model.

**4. Every review above light comes from two vendors.** Astra reviews correctness in medium and heavy and is
the second architect: it is strongest exactly where the Claude models are weakest (algorithms, operations,
scientific reasoning), 2–3× faster per task, and a reviewer from another vendor fails in different ways than
the writer. The Claude reviewer is whichever top model did not write: Fable in medium, Opus in heavy and ultra.

**5. Ultra checks correctness twice.** Correctness is reviewed by Opus and by Astra independently, both at
`xhigh`; a defect has to get past both. Ultra is also the only mode whose effort ceiling is `max`.

**6. Light stays cheap, and Sol reads for everyone.** Sol is 5–10× cheaper per task and the fastest; for an
exact change of a few lines that is the right trade, and the lead reads the diff itself. Front-end in light
goes to Sonnet. The same qualities make Sol the scout in every other mode.

**7. The security lane runs on Astra.** Claude models route or decline some security-flavoured requests through
safety classifiers; a review lane that can be declined is not a dependable gate.

## The resulting table

| | light | medium | heavy | ultra |
|---|---|---|---|---|
| Context | the lead's own | scout `sol:medium` | scout `sol:medium` | scout `sol:high` |
| Architect | the lead | the lead | `fable:high` ∥ `astra:high` | `fable:xhigh` ∥ `astra:xhigh`, then cross-critique |
| Backend writer | `sol:medium` | `opus:medium` | `fable:high` | `fable:xhigh` |
| Frontend writer | `sonnet:medium` | `opus:medium` | `fable:high` | `fable:xhigh` |
| Review: architecture | the lead | `fable:medium` | `opus:high` | `opus:high` |
| Review: correctness | the lead | `astra:medium` | `astra:high` | `opus:xhigh` ∥ `astra:xhigh` |
| Review: security | — | — | — | `astra:high` |
| Effort ceiling | `high` | `high` | `xhigh` | `max` |

Other roles: read-only consultations on `sonnet:medium`, UI design on `opus:medium`, the browser pass on
`astra:medium` (strong computer-use scores, and the browser tooling is wired to the Codex CLI).

Which subscriptions each mode draws on: light — Codex and Claude; medium, heavy and ultra — Claude, Fable and
Codex, with the parallel design and review phases split across them.

### Why these effort levels

- **Vendor defaults are the baseline.** OpenAI ships Astra at `medium`; Anthropic ships Opus 5.5 at `medium`
  and recommends starting Sonnet 5.5 at `medium` for agentic coding. The independent Vals scores above were,
  as far as can be told, measured at those shipped levels — so "medium" is the effort the published numbers
  describe. Sol is the exception: it ships at `low`, and the published sweep shows a large gap between `low`
  and `medium` (53.6 against 60.6), so it runs at `medium`.
- **Fable at `high` already equals Opus at its maximum**, for roughly half the output tokens (98,000 against
  173,000 per task). That makes `fable:high` the efficient strong setting, used for heavy. Its margin over
  Opus opens at `xhigh`, which is what ultra pays for.
- **Returns flatten near the top and cost does not.** In the published sweep `xhigh` adds about two points over
  `high` for roughly 47% more cost. In our own runs of the same toy task, a design pass took 110 seconds at
  `xhigh` and 349 seconds at `max`. So `max` is an escalation, not a default: it is used when a correction has
  demonstrably stalled, which is the "measurement shows headroom" condition Anthropic sets for it.
- **Reading is cheap work.** Anthropic's own guidance is to move reading-heavy sub-tasks to a cheaper worker.
  The scout runs on Sol — the cheapest and fastest of the five, with the largest default context — and a
  recheck, which reads only a delta, runs one step below the review.

## What the data does not show

- **No public benchmark measures code review or architecture critique.** Reviewer choices are inferred from
  coding and reasoning results plus the independence rule. So is the rule itself — that the strongest model
  should create rather than review: the public data measures doing the work, not checking it.
- **No equal-effort comparison across vendors.** Vals does not report effort; the Sol–Sonnet gap would likely
  narrow with Sol at high effort.
- **Vendor tables favour the vendor.** Fable's lead over Opus comes from Anthropic's own table, and Anthropic
  chose which efforts to pair. Independent sources put the two level on everyday terminal work.
- **`max` is expensive.** One measurement puts Fable 5.2 at its top tier at roughly twice the cost per task of
  the level below, which is why no default uses it.
- **Subscription limits are not list prices.** Whether Fable draws on its own limit or shares Opus's depends on
  your plan; set `pool` accordingly.
- **Your repository is not a benchmark.** If these defaults disagree with what you observe, change them.

## Sources

- [Vals AI: Claude Sonnet 5.5 vs GPT-6.1 Sol](https://www.vals.ai/comparisons/anthropic_claude-sonnet-5-5-vs-openai_gpt-6.1-sol)
- [Vals AI: Claude Opus 5.5 vs GPT-6 Astra](https://www.vals.ai/comparisons/anthropic_claude-opus-5-5-vs-openai_gpt-6-astra)
- [DataCamp: GPT-6.1 Sol vs Claude Sonnet 5.5](https://www.datacamp.com/blog/gpt-6-1-sol-vs-claude-sonnet-5-5)
- [DataCamp: GPT-6 Astra vs Claude Opus 5.5](https://www.datacamp.com/blog/gpt-6-astra-vs-claude-opus-5-5)
- [DataCamp: Claude Fable 5.2 and Mythos 5.2](https://www.datacamp.com/blog/claude-fable-5-2-and-mythos-5-2)
- [SmartScope: Claude Fable 5.2 score table](https://smartscope.blog/en/blog/claude-fable-5-2-score-table-all-14-2026/)
- [Singularity Moments: Fable 5.2 on daily dev work](https://singularitymoments.com/content/fable-52-delivers-huge-math-gains-but-stumbles-on-daily-dev-work/)
- [Webiano: Fable 5.2 vs Opus 5.5 on FrontierCode](https://webiano.digital/claude-fable-5-2-beats-opus-5-5-by-only-3-6-points-on-frontiercode/)
- [philippdubach.com: performance, cost and speed by effort](https://philippdubach.com/posts/llm-performance-cost-speed-sweet-spot/)
- [alphaXiv 2607.02436: reasoning effort and first-try reliability](https://www.alphaxiv.org/abs/2607.02436)
- Anthropic model reference and effort guidance (bundled with Claude Code), and the model list shipped by the
  Codex CLI (`~/.codex/models_cache.json`).
