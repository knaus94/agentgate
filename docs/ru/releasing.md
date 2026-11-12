# Релизы

[English](../releasing.md)

## Опубликовать версию

```sh
# 1. обновить CHANGELOG.md
git commit -am "chore(release): v0.2.0"
git tag v0.2.0
git push origin main v0.2.0
```

Пуш тега `v*` запускает [.github/workflows/release.yml](../../.github/workflows/release.yml):

1. `go vet` и `go test` на помеченном коммите;
2. статические сборки для `linux/amd64` и `linux/arm64` с вшитой версией;
3. `agentgate_linux_<arch>.tar.gz` для каждой плюс `checksums.txt`;
4. GitHub Release с автоматическими заметками.

С машины разработчика ничего не публикуется. Тег, не прошедший тесты, ничего не выпускает.

Номера версий — семантические: изменение схемы плана, формата состояния или входа команды — минорная версия
до 1.0 и мажорная после.

## Как это получают пользователи

Первая установка:

```sh
curl -fsSL https://raw.githubusercontent.com/knaus94/agentgate/main/scripts/install.sh | sh
```

Обновление:

```sh
agentgate update --check   # есть ли версия новее?
agentgate update           # скачать, проверить, заменить, обновить команду /dev
```

`update` скачивает архив для текущей платформы, сверяет его SHA-256 с `checksums.txt`, атомарно заменяет
бинарник и запускает `install` нового бинарника, чтобы команда `/dev` ему соответствовала. Идущая задача не
страдает: её формат состояния читается новым бинарником.

Закрепить версию: `AGENTGATE_VERSION=v0.2.0 sh install.sh`.
Установить в другое место: `AGENTGATE_BIN=/usr/local/bin sh install.sh`.
Зеркало релизов: `AGENTGATE_BASE_URL=https://mirror.example/releases sh install.sh`.

## Форки

Репозиторий релизов вшивается при сборке. Собрать со своим:

```sh
go build -ldflags "-X github.com/knaus94/agentgate/internal/update.Repo=you/agentgate \
                   -X github.com/knaus94/agentgate/internal/update.Version=0.2.0" ./cmd/agentgate
```

и поменять `REPO` в `scripts/install.sh`.
