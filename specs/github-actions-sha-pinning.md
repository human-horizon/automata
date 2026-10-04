# Неподвижные refs для GitHub Actions

## Контекст и цель

`.github/workflows/ci.yml` сейчас использует major tags (`actions/checkout@v4`, `actions/setup-go@v5`, `golangci/golangci-lint-action@v8`). Для защиты от перемещения тегов каждый action должен ссылаться на полный 40-символьный commit SHA, а версия остаётся в комментарии, чтобы обновления можно было проверить и обслуживать Dependabot.

## Проверенные upstream refs

Проверены official GitHub refs и commit verification:

| Action | Release | Commit SHA | Проверка |
|---|---|---|---|
| `actions/checkout` | `v7.0.1` | `3d3c42e5aac5ba805825da76410c181273ba90b1` | official tag указывает на commit; commit signature verified |
| `actions/setup-go` | `v7.0.0` | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` | official tag указывает на commit; commit signature verified |
| `golangci/golangci-lint-action` | `v9.3.0` | `ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a` | official annotated tag signature verified |

Версии и SHAs подтверждены через GitHub API upstream repositories. Не использовать SHA, не полученный из official ref/release.

## План изменения

В `.github/workflows/ci.yml` заменить только action refs на `uses: owner/repo@<полный SHA> # vX.Y.Z`. Оставить `permissions: contents: read`, workflow triggers и тестовые команды без изменений. Сохранить Dependabot `github-actions` ecosystem. GitHub сообщает, что Dependabot обновляет version comments для action refs, закреплённых SHA: <https://github.blog/changelog/2022-10-31-dependabot-now-updates-comments-in-github-actions-workflows-referencing-action-versions/>.

PR #4/#5/#7 обновляют теги действий, но их ветки должны быть актуализированы до merge из-за strict branch protection. GitHub отказал OAuth credential в `workflow` scope при попытке update-branch PR #4; не обходить ограничение и не менять remote до получения авторизованного scope.

## Проверки приёмки

- Все `uses:` refs в CI — ровно 40 hex-символов с официальным release comment; иных third-party actions нет.
- Повторно сверить каждый SHA с official tag/commit API.
- Проверить release notes и exact PR checks на обновлённом head перед отдельным merge.
- Проверить, что Dependabot распознаёт version comments и продолжает предлагать updates.
- CI `quality`, diff check и security review проходят; не создавать локальные коммиты.
