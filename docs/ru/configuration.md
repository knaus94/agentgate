# Настройка

[English](../configuration.md)

Три слоя, каждый следующий переопределяет предыдущий по ключам:

1. встроенная таблица ([internal/assets/config.json](../../internal/assets/config.json))
2. `~/.config/claude-dev/config.json` — ваша, на этой машине
3. `.claude-dev.json` в репозитории — проектная

`claude-dev config show` печатает итог слияния, `claude-dev modes` — его же таблицей.
Почему значения по умолчанию такие — в [Почему именно эти модели](model-selection.md).

## Модели

```json
{
  "models": {
    "opus":   {"provider": "claude", "model": "claude-opus-5-5",   "pool": "claude"},
    "sonnet": {"provider": "claude", "model": "claude-sonnet-5-5", "pool": "claude"},
    "fable":  {"provider": "claude", "model": "claude-fable-5-2",  "pool": "fable"},
    "sol":    {"provider": "codex",  "model": "gpt-6.1-sol",       "pool": "codex"},
    "astra":  {"provider": "codex",  "model": "gpt-6-astra",       "pool": "codex"}
  }
}
```

- Ключ — это **псевдоним**. Режимы ссылаются на псевдонимы, поэтому замена модели — одна правка.
- `provider` — CLI, который её запускает: `claude` или `codex`.
- `pool` — название квоты, из которой она расходуется. Это метка для вас и для `watch`; дайте двум моделям
  один пул, если у них общий лимит.

Привязать псевдоним к другой модели:

```sh
claude-dev config set models.sol.model gpt-6.1-sol
```

Добавить модель и использовать её:

```sh
claude-dev config set models.haiku '{"provider":"claude","model":"claude-haiku-5-5","pool":"claude"}'
claude-dev config set modes.light.backend haiku:medium
```

### Только Claude или только Codex

Псевдонимы — просто имена. С одним провайдером направьте каждый псевдоним на доступные модели:

```sh
claude-dev config set models.sol   '{"provider":"claude","model":"claude-sonnet-5-5","pool":"claude"}'
claude-dev config set models.astra '{"provider":"claude","model":"claude-opus-5-5","pool":"claude"}'
claude-dev config set reviewers_excluded '[]'
```

Ревьюер по-прежнему должен быть другой моделью, чем писатель бэкенда, так что оставьте минимум две разные.

## Маршруты

Маршрут — это `<псевдоним>:<усилие>`, усилие одно из `low`, `medium`, `high`, `xhigh`, `max`:

```sh
claude-dev config set modes.medium.backend sonnet:high
claude-dev config set ui_designer opus:high
```

| Ключ | Для чего |
|---|---|
| `modes.<режим>.scout` | дешёвый маршрут, собирающий контекстный пакет; `""` отключает этап |
| `modes.<режим>.escalate_to` | наибольшее усилие, до которого может дойти застрявшее исправление или трудный поток |
| `modes.<режим>.architecture` | маршруты дизайна (два — для раунда критики) |
| `modes.<режим>.backend`, `.frontend` | писатели по домену потока |
| `modes.<режим>.review` | полосы ревью: `[{"axes": [...], "route": "..."}]`; оси — `correctness`, `architecture`, `security` |
| `modes.<режим>.blocking` | уровни, блокирующие завершение |
| `modes.<режим>.critique` | перекрёстная критика после дизайнов |
| `modes.<режим>.architecture_timeout_s`, `.writer_timeout_s`, `.reviewer_timeout_s` | потолок одного процесса дизайна, писателя или ревьюера; последние два план может переопределить |
| `ui_designer`, `visual_reviewer`, `consultant` | дизайнер интерфейса, браузерный ревьюер, консультации |

## Замены

```json
{"fallbacks": {"fable": ["opus", "astra"], "opus": ["fable", "astra", "sonnet"]}}
```

Когда провайдер сообщает об исчерпанном лимите, полоса повторяется на этих псевдонимах по порядку, с тем же
усилием. Замена пропускается, если она писала проверяемый код. Каждая замена попадает в сводку задачи. Другие
сбои на другую модель не переносятся.

`reviewers_excluded` — псевдонимы, которым запрещено ревьюить (по умолчанию `sol`, слабейший из пяти на
задачах в реальных репозиториях).

## Прочие ключи

| Ключ | По умолчанию | Смысл |
|---|---|---|
| `max_parallel` | 4 | процессов моделей одновременно, если в запросе нет `-N` |
| `worker_memory_mib` | 512 | свободной памяти на воркера; лимиты cgroup учитываются |
| `retention_days` | 14 | завершённые задачи старше этого срока удаляются |
| `playwright_mcp` | `~/.local/share/claude-dev/playwright-mcp/…/cli.js` | браузерный инструмент для необязательной визуальной проверки |

## Сброс

```sh
claude-dev config unset modes.medium.backend   # вернуть встроенное значение
rm ~/.config/claude-dev/config.json           # вернуть всё по умолчанию
```

Правка, которая сделала бы таблицу некорректной, отклоняется, и ничего не меняется.

## Браузерная проверка (необязательно)

```sh
mkdir -p ~/.local/share/claude-dev/playwright-mcp && cd ~/.local/share/claude-dev/playwright-mcp
npm init -y && npm install @playwright/mcp && npx playwright install chromium
```

Ревьюеру доступны только адреса, которые разрешаются в loopback.
