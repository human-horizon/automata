# Корректирующий hardening pass PR #2

## Контекст

После hardening pass остались точечные регрессии на границах профиля, cleanup, frontmatter и watcher/cache. Они находятся в существующей ветке `fix/main-hardening-2026-09-23`; отдельный PR и merge не нужны.

## Цель

Закрыть все перечисленные регрессии тестами, сохранить уже принятые lifecycle-инварианты и доставить проверенный corrective commit в существующий PR #2.

## Что изменится

1. `main.go`, `main_view_test.go` — явный canonical `AUTOMATA_PROFILE` для каждого запущенного Pi, в том числе `default` и `PI_CMD`.
2. `internal/tree/model.go`, `internal/tree/tree_test.go` — копирование active-session maps на входе, без разделяемого владения и с надёжным rollback.
3. `main_lifecycle.go`, `main_lifecycle_test.go` — завершение подготовленных job plans после зафиксированной остановки runtime даже при ошибке persistence; сохранение всех ошибок.
4. `internal/ui/chat_panel.go`, `internal/ui/chat_panel_test.go`, `main.go`, lifecycle tests — продолжение host cleanup после committed stop failure; удаление familiar-вкладки только при committed cleanup и сохранение ошибок в возвращаемом/logged результате. Ошибки preflight по-прежнему оставляют вкладку и runtime нетронутыми.
5. `internal/kanban/kanban.go`, tests и при необходимости `go.mod`/`go.sum` — YAML-node round trip для неизвестного вложенного frontmatter; строгая валидация статусов при чтении и каждой записи.
6. `internal/status/status.go`, tests — cache identity учитывает замену файла при совпадающих mtime и размере.
7. `internal/ui/kanban_panel.go`, tests — обработка fsnotify `Errors` и закрытых каналов с диагностикой, переинициализацией watcher и обновлением доски.
8. `internal/ai-knowledge/context/reader.go`, `internal/ai-knowledge/jobs/reader.go` и tests — единый legacy resolver: профиль из префикса session ID, иначе `AI_PROFILE`, иначе canonical `default`; explicit profile APIs остаются независимы от env.
9. `CONTEXT.md` и эта спецификация — записи о решениях, regressions и итоговых проверках.

## Детали реализации

- `piLaunch` всегда добавляет `AUTOMATA_PROFILE=paths.ProfileSlug(a.profile)`. `PI_CMD` меняет executable, но не profile environment; прочие правила запуска не меняются.
- `Tree.SetActiveSessionsInMemory` сохраняет собственную копию входной map. Ошибка `SaveState` восстанавливает отдельный snapshot без alias на map вызывающей стороны.
- После runtime commit lifecycle kernel собирает persistence и job-cleanup failures, пытается завершить каждый подготовленный job plan и возвращает committed error через `errors.Join`.
- Для familiar close uncommitted/preflight error остаётся fail-closed. После committed stop выполняются все возможные host cleanup операции; committed-warning передаётся UI как typed error, вкладка удаляется, а ошибка остаётся доступной caller/log.
- Kanban frontmatter разбирается в `yaml.Node`; mutations обновляют только известные scalar fields и сохраняют остальные node trees. Допустимые статусы: `todo`, `pending`, `progress`, `done`; отсутствующий status по-прежнему означает `todo`. Некорректный status отклоняется, а не отображается как `todo`.
- Status cache использует `os.SameFile` вместе с существующими метаданными файла, чтобы обнаруживать атомарную замену с теми же mtime/size.
- Kanban watch command ждёт `Events` и `Errors`. Ошибка/закрытие watcher channel вызывает логирование, восстановление watcher и принудительную перезагрузку, без второго читателя на одном watcher.
- Legacy `Read`/`List`/`RunningCount` и их cached convenience methods вызывают общий по смыслу resolver профиля; `ReadForProfile`/`ListForProfile`/`RunningCountForProfile` не меняют явный контракт.

## Критерии приёмки

- [x] Pi child получает канонический `AUTOMATA_PROFILE` для default и именованного профиля при обычном запуске и через `PI_CMD`.
- [x] Active-session maps не разделяют изменяемую память вызывающей стороны с Tree; rollback после persistence failure сохраняет прежний набор.
- [x] Подготовленные jobs завершаются после committed runtime stop даже при ошибке сохранения active sessions; persistence и job errors наблюдаемы.
- [x] Committed familiar cleanup пытается удалить JSONL и запись familiar, удаляет вкладку и сохраняет ошибки; uncommitted preflight failure оставляет вкладку.
- [x] Kanban mutation сохраняет вложенные mappings/sequences и значения типов YAML; invalid statuses отклоняются на read/write/update.
- [x] Status cache замечает replacement при неизменных mtime и size.
- [x] Ошибка и закрытие каналов Kanban watcher приводят к восстановлению watcher и reload.
- [x] Legacy context/jobs APIs выбирают профиль одинаково по префиксу, `AI_PROFILE` и default; explicit APIs изолированы от `AI_PROFILE`.
- [x] Адресные тесты, `gofmt -l .`, `go vet ./...`, `go mod verify`, `go test ./... -count=1 -p 1`, `git diff --check` и два воспроизводимых build проходят.
- [ ] Новый commit находится в существующем PR #2, локальный HEAD совпадает с remote; CI нового HEAD зелёный, PR не смёржен.
