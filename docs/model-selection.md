# Model routing and independence

[Русский](ru/model-selection.md)

Model names are configuration, not an architectural dependency. The shipped table is a starting profile for
accounts that have the named models. It is not a ranking, a claim of benchmark superiority, or a promise that
your subscription exposes every route. Inspect the effective profile with `agentgate modes` and validate
changes with `agentgate config validate`.

## Invariants before preferences

1. A reviewer cannot use a model that authored any part of the change, including a failed partial writer,
   a fallback writer, or a writer in an earlier stage. Comparing actual model identities prevents aliases
   from bypassing this rule.
2. A fallback can spend only the accounts and aliases declared in configuration. Quota exhaustion may
   trigger an alternate; a successful answer mentioning a rate limit is not an exhaustion event.
3. Continuing a worker keeps both its provider session identifier and its actual lane directory. Account
   rotation may retry that session within the same provider; if the CLI refuses it, the error is explicit.
   Model substitution is disabled for a resumed session.
4. The selected effort is explicit. A recheck can be cheaper because it sees a delta; escalation is tied to
   lack of progress rather than to an unsupported claim about a universal best model.
5. The dashboard and receipts show the actual routes and substitutions. Model independence is mandatory;
   vendor diversity depends on the configured profile and the available fallbacks.

## Mixed-provider defaults

| Role | light | medium | heavy | ultra |
|---|---|---|---|---|
| Scout | lead context | sol:medium | accepted architecture | accepted architecture |
| Architecture | lead | lead | fable:high + astra:high | fable:xhigh + astra:xhigh, cross-critique |
| Backend writer | sol:medium | opus:medium | opus:high | opus:xhigh |
| Frontend writer | sonnet:medium | opus:medium | opus:high | opus:xhigh |
| Architecture and simplicity review | lead | fable:medium | fable:high | fable:high |
| Correctness review | lead | astra:medium | astra:high | fable:xhigh + astra:xhigh |
| Security review | — | — | — | astra:high |
| Escalation ceiling | high | high | xhigh | max |

Heavy and ultra use Opus for both implementation domains. The previous table used Astra for frontend work
and also for correctness review, which conflicted with author independence in mixed tasks. Keeping Fable
and Astra available as reviewers makes the standard routes coherent before any fallback is needed.

The table intentionally does not encode prices, subscription multipliers or benchmark scores. Those change
independently of the gate and do not establish the best route for a particular repository. Use observed
completion time, correction rounds and quota use on representative tasks to tune your own profile.

## One provider

`agentgate setup install --lead codex` selects the Codex-only profile. It retains independent model identities
inside the same provider: Astra writes substantial tasks, Sol reviews, and Terra supplies another role when
configured. See [Codex](codex.md) and the actual profile in
[internal/assets/profiles/codex.json](../internal/assets/profiles/codex.json).

A single-provider setup cannot provide cross-vendor review. It can still enforce independent sessions,
different author/reviewer model identities, current checks and evidence-bound completion. A single-model
setup is suitable only for light mode, where the lead reviews the change.

## Change a route

```sh
agentgate config set models.sol.model gpt-6.1-sol
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
