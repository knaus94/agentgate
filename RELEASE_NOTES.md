# AgentGate 0.14.1 — interactive clarification and planning fixes

Исходная база: `knaus94/agentgate`, коммит `d1185635e89587683acfe90b047a29e0c70e7c72` (0.13.0).
Версия включает исходники и автономный установочный архив; результаты выпускной проверки приведены ниже.

## Исправления 0.14.1

Лид использует штатные интерактивные вопросы при наличии поддержки в клиенте. Для Codex Default-mode может
потребоваться opt-in флаг, описанный в docs/ru/codex.md; установщик не включает его глобально без решения
пользователя. Сами формы в Zed/Claude требуют проверки в интерактивной сессии, headless-тесты не доказывают
их отображение.

Многоэтапные read-only планы теперь сохраняются без ошибочного требования убрать stages. Сохранение не
разрешает реализацию; launch всё ещё отклоняет действие plan. После неудачной коррекции prepare выдаёт
публичную диагностику вместо предложения разбирать приватное состояние. Эти пути и установка интеграций
проверены узкими регрессиями без новых платных модельных вызовов.

## Что находится в архиве

- Полные исходники Go и встроенного Node design toolkit, включая тесты.
- Готовые статические бинарники `bin/linux-amd64/agentgate` и `bin/linux-arm64/agentgate`.
- Локальный установщик, Makefile, упаковка, CI и workflow будущего релиза.
- Руководства на русском и английском, описание архитектуры, миграции и границ изоляции.
- `RELEASE.json` с происхождением сборки и `SHA256SUMS` для каждого включённого файла.

## Установка

```sh
cd agentgate-0.14.1
sh scripts/install-local.sh --lead both
agentgate setup doctor
agentgate config validate
agentgate modes
```

Используйте `--lead codex`, `--lead claude` или `--lead both`. Установщик копирует локальный бинарник и не
скачивает зависимости. Для установки только бинарника: `--no-integrations`. Основной runtime требует
Linux/WSL2, Git, diff, Bubblewrap и настроенные CLI выбранных провайдеров. Design workflow дополнительно
нуждается в Node 20+; браузерные части — в Playwright/Chromium.

## Основные изменения

**Архитектура.** CLI, состояние задачи, жизненный цикл, доказательства, worktree, аккаунты, provider adapters,
конфигурация и терминальная панель получили отдельные модули. Сохранён один Go-бинарник без внешних Go-модулей.
Общий парсер и атомарные операции используются в Node-командах.

**Завершение задачи.** Старый nonzero baseline больше не оправдывает новый сбой. Ошибки fingerprint/state
не превращаются в пустой результат или устаревшую успешную проверку. Изменение содержимого или обязательных
доказательств инвалидирует квитанции. Авторы всех попыток и предыдущих стадий исключаются из ревью.

**Восстановление.** `task resume` и `task continue` продолжают незавершённый раунд; завершённые потоки,
provider sessions и реальные директории запуска сохраняются. Блокирующий вопрос требует ответа.
Отмена ждёт завершения процесса и проверяет его идентичность, а не только PID.

**Git.** Применение и коммит разделены. Commit landing не присоединяет посторонние staged-файлы или
пользовательские правки тех же файлов. Удаление worktree ограничено управляемым каталогом и веткой задачи.
Отказ commit hook не записывается как успешный коммит.

**Аккаунты и изоляция.** Исправлены окна квот, дубликаты подписок, кэш ошибок probes, конфликты окружения и
fallback-сессии. Воркеры получают приватную копию credentials; refresh сверяется перед обратной записью.
MCP writable paths не выходят через symlink за checkout. Figma-профили маскируются после остальных mounts.
Подробная модель доверия: [SECURITY.md](SECURITY.md).

**CLI и TUI.** Группы `task`/`setup`, справка по командам, `config validate`, чистый `--help`, строгие флаги и
JSON-вход, `watch --json`, `--no-color`/`NO_COLOR`, адаптация к узкому терминалу, безопасный вывод текста,
корректная обработка клавиш и восстановление терминала. Старые плоские команды сохранены.

**Figma и design toolkit.** Добавлен явный официальный REST-транспорт с привязкой рендеров к версии.
Браузерный адаптер сохранён. Пакеты публикуются только полностью; опись и байты проверяются независимо.
Фикстуры учитывают variables, пропуски видны как ошибки. Управление стендом имеет timeout, журналы и
проверяемое владение процессами. Исправлена проверка пропавшей заливки и чтение современных CSS-цветов.

**Поставка.** Атомарная установка/обновление с проверкой контрольных сумм. Автоматическое обновление
сравнивает SemVer и не заменяет 0.14.0 более старой 0.13.0. Архив собирается воспроизводимым скриптом из
явного списка файлов, без `.git`, `node_modules`, профилей и учётных данных.

## Проверено и что требует внешнего окружения

Точные команды, результаты и ограничения приведены в [docs/validation.md](docs/validation.md).
В текущей среде прошли Go race-тесты, настоящие Bubblewrap/CLI-сценарии, 18 Node core-проверок и полный
Chromium workflow. Выполнены живой Codex-цикл с независимым ревью и браузерный экспорт Figma на защищённой
копии профиля. Проверены передача контракта между потоками, BLOCKED/answer, адресное исправление в той же
сессии и повторное ревью с тем же finding ID; сложные протокольные случаи используют фиктивные провайдеры
для экономии квоты. Двухэтапная heavy-задача с общей архитектурой дошла до интеграционной приёмки. Отдельная маленькая backend-задача на реальных моделях прошла три этапа
(контракты → бизнес-логика → HTTP) и общую приёмку за восемь вызовов, включая recheck после
корректного отказа из-за неполных доказательств.

При проверке исправлены три дополнительных runtime-дефекта: чтение конфига больше не требует writable
state; продолжение/ответ исполнителю перепроверяет архитектуру целой задачи; повреждённое состояние
соседней сессии не освобождает её файлы. Браузерная фикстура адаптирована к строгому запрету дублирующих флагов.

Установка из ZIP в путь с пробелами и отказ при повреждённом бинарнике проверены. ARM64 собран, но нативно
на ARM64-оборудовании не запускался. Живой Claude inference, исчерпание квот и реальный REST-токен Figma
в этот прогон не входили. Проверки не являются обещанием отсутствия любых возможных дефектов.

Перед переносом действующей установки прочитайте [миграцию](docs/migration.md); старые квитанции могут
потребовать новой проверки. Маршруты моделей проверьте по доступу вашей подписки через конфигурацию.

## English overview

This archive delivers the refactored 0.14.1 source and Linux binaries, based on the commit above. It keeps
the local CLI/provider integration model and reorganizes the code around lifecycle, evidence, execution,
configuration and presentation boundaries. The most consequential changes prevent stale/failing evidence
from authorizing completion, protect unrelated Git changes, preserve interrupted rounds and credential
refreshes, and make actual author/reviewer identities explicit.

CLI compatibility is retained through flat commands alongside `task`/`setup` aliases. The design toolkit
adds documented Figma REST export, verified atomic packages, exact-variable fixtures and owned stand
cleanup. Distribution includes checksums, offline installation and reproducible packaging. Read
[README.md](README.md), [architecture](docs/architecture.md), [migration](docs/migration.md),
[research decisions](docs/research.md) and the [validation record](docs/validation.md) for details and
verified limits, including the distinction between live provider calls and scripted protocol scenarios.
