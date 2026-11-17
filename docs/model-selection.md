# Model routing and independence

[Русский](ru/model-selection.md)

Model names are configuration, not an architectural dependency. The shipped table is a starting profile for
accounts that have the named models. It is not a ranking, a claim of benchmark superiority, or a promise that
your subscription exposes every route. Inspect the effective profile with `agentgate modes` and validate
changes with `agentgate config validate`.

## Invariants before preferences

1. Every role runs on the model the table names for it. Which model reviews is the user's choice, also the
   model that writes the code; `reviewers_excluded` is the only exclusion.
2. A fallback can spend only the accounts and aliases declared in configuration; the built-in table declares
   none, so an exhausted limit stops the call until it returns. A successful answer mentioning a rate limit is
   not an exhaustion event.
3. Continuing a worker keeps both its provider session identifier and its actual lane directory. Account
   rotation may retry that session within the same provider; if the CLI refuses it, the error is explicit.
   Model substitution is disabled for a resumed session.
4. The selected effort is explicit. A recheck can be cheaper because it sees a delta; escalation is tied to
   lack of progress rather than to an unsupported claim about a universal best model.
5. The dashboard and receipts show the actual routes and substitutions, each with its reason: no account of
   the provider has room (and when one is back), every account is kept for the lead, or the route is kept from
   review.

## Mixed-provider defaults

| Role | light | medium | heavy | ultra |
|---|---|---|---|---|
| Preparation | astra:medium | astra:medium | astra:medium | astra:medium |
| Architecture | opus:medium | opus:high | opus:high + astra:xhigh, closed by opus | fable:high + astra:max, cross-critique, closed by fable |
| Backend writer | sol:high | sol:xhigh | astra:high | astra:xhigh |
| Frontend writer | sol:high | sol:xhigh | astra:high | astra:xhigh |
| Architecture and simplicity review | opus:medium, one lane with correctness | opus:high, one lane with correctness | opus:high | fable:high |
| Correctness review | the same lane | the same lane | fable:high | fable:xhigh + opus:xhigh |
| Security review | — | — | — | opus:high |
| UI designer | opus:medium | opus:medium | opus:high | opus:xhigh |
| Consultant | astra:medium | astra:medium | astra:medium | astra:medium |
| Browser check | astra:high | astra:high | astra:high | astra:high |
| Escalation ceiling | xhigh | xhigh | xhigh | max |

Claude models own the design and the review; Codex models plan, write, answer consultations and check the
browser. A Claude model closes every design, and no reviewer reviews code its own model wrote; Astra checks in
the browser the frontend it wrote in heavy and ultra. When the Codex limit is exhausted on every account,
Astra and Sol fall back to Opus at the same effort; Fable falls back to Opus. Opus has no fallback: when it is
exhausted, the call stops and the lead pauses the task until it returns, rather than letting Codex review.

The table intentionally does not encode prices, subscription multipliers or benchmark scores. Those change
independently of the gate and do not establish the best route for a particular repository. Use observed
completion time, correction rounds and quota use on representative tasks to tune your own profile.

## One provider

`agentgate setup install --lead codex` selects the Codex-only profile. It retains independent model identities
inside the same provider: Astra writes substantial tasks, Sol reviews, and Terra supplies another role when
configured. See [Codex](codex.md) and the actual profile in
[internal/assets/profiles/codex.json](../internal/assets/profiles/codex.json).

A single-provider setup cannot provide cross-vendor review. It can still enforce independent sessions,
current checks and evidence-bound completion, and different models for writing and review where the profile
names them. A single-model
setup is suitable only for a mode configured with `review: []`, where the lead reviews the change.

## Change a route

```sh
agentgate config set models.sol.model gpt-6.2-sol
agentgate config set modes.medium.backend sol:high
agentgate config set modes.medium.frontend sol:high
agentgate config validate
agentgate modes
```

Before assigning a writer to a reviewer model, change the review routes too. If all eligible reviewers are
excluded or exhausted, the task stops with a routing error; it must not approve its own code. Configuration
validation checks structure and configured relationships, not live access to a paid provider model.

For an experiment, change one role at a time and compare similar tasks. Track elapsed wall time, failed
checks, reopened findings and whether the final result met the original requirements. Do not infer token
cost or account capacity from the number of parallel processes alone.

## Protocol references

The adapters follow the provider CLIs rather than reimplementing their authentication or billing:

- [Codex non-interactive execution](https://developers.openai.com/codex/noninteractive/)
- [Codex CLI reference](https://developers.openai.com/codex/cli/reference/)
- [Claude Code CLI reference](https://code.claude.com/docs/en/cli-reference)
- [Claude Code headless usage](https://code.claude.com/docs/en/headless)

A provider can change its supported model identifiers, effort values or JSON events. Unit and integration
fixtures exercise the adapter contract; a live smoke test with your installed CLI and account is still the
last compatibility check. A protocol error is surfaced, never converted to a successful worker result.
