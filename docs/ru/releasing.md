# Сборка и публикация релиза

[English](../releasing.md)

## Полный архив локально

```sh
make check
npm ci --prefix design-kit --ignore-scripts --no-audit --no-fund
node design-kit/node_modules/playwright-core/cli.js install chromium
make design-browser
make package VERSION=0.14.1
```

Результат: `dist/agentgate-0.14.1.zip` и файл `.sha256`. Внутри исходники, документация, статические бинарники
Linux amd64/arm64, `scripts/install-local.sh`, `RELEASE.json`, опись `SHA256SUMS`. Упаковщик использует явный
список и фиксированное время ZIP. `.git`, node_modules, приватные профили и кэши не включаются.
`SOURCE_DATE_EPOCH` задаёт время релиза при необходимости.

Проверка перед передачей:

```sh
unzip dist/agentgate-0.14.1.zip
cd agentgate-0.14.1
sha256sum -c SHA256SUMS
sh scripts/install-local.sh --prefix "$HOME/.local/bin" --no-integrations
```

Создание архива ничего не публикует. Недоступные проверки браузера или провайдера не считаются пройденными:
запишите ограничения окружения вместе с результатом.

## Публикация через GitHub

Обновите версию, changelog и release notes, включите все новые/изменённые файлы в коммит и выполните проверки.
После разрешения сопровождающего push тега `v*` запускает
[release.yml](../../.github/workflows/release.yml). Workflow проверяет форматирование, vet, race-тесты,
настоящие Chromium-сценарии, собирает обе архитектуры, проверяет архив и локальную установку, публикует:

- `agentgate_linux_<arch>.tar.gz` и совместимые архивы `claude-dev`;
- `checksums.txt` для обновления;
- полный ZIP с исходниками/бинарниками и его SHA-256.

Ошибка обязательного шага отменяет публикацию. Настоящие аккаунты моделей и доступ к Figma в CI не
используются; протокольные фикстуры не являются сертификацией live-провайдера.

## Установка и обновление

Онлайн-установщик получает опубликованный релиз:

```sh
curl -fsSL https://raw.githubusercontent.com/knaus94/agentgate/main/scripts/install.sh | sh
agentgate update --check
agentgate update
```

Автообновление разрешено только на строго более новую корректную SemVer-версию. Учитывается порядок
prerelease, build metadata не делает версию новее. Неизвестная/dev-версия автоматически не заменяется.
Размер загрузки ограничен, проверяется точный SHA-256, замена выполняется через случайный временный файл
и атомарный rename. Затем новый бинарник обновляет установленные интеграции лида.

Завершите или явно остановите активные операции перед заменой их бинарника. Читайте миграцию конкретного
релиза: совместимость состояния нельзя обещать безусловно для всех будущих версий.

Настройки установщика: `AGENTGATE_VERSION=v0.14.1`, `AGENTGATE_BIN=/some/bin`,
`AGENTGATE_BASE_URL` для разрешённого зеркала релизов. Закреплённая версия — явный выбор и может быть старее;
обычный `update` не должен случайно понижать новую локальную сборку.

## Форки

Путь Go-модуля остаётся `github.com/knaus94/agentgate`. Репозиторий релизов переопределяется при линковке:

```sh
go build -trimpath -buildvcs=false -ldflags '-X github.com/knaus94/agentgate/internal/update.Repo=you/agentgate -X github.com/knaus94/agentgate/internal/update.Version=0.14.1' ./cmd/agentgate
```

Workflow получает путь символов из `go list -m`, а репозиторий — из `GITHUB_REPOSITORY`.
Для постоянного форка измените default `REPO` в онлайн-установщике.
