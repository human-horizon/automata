# Канонические пути сессий Automata

## Контекст
`main.go::(*App).sessionBaseDir` обходит `internal/paths.BaseDir` и `AI_DATA_HOME`; для пустого профиля выбирается неверный путь `<base>/sessions`. `internal/status` дублирует resolver и `slugify`, из-за чего default- и Unicode-пути расходятся. Rename и status watcher наследуют ту же ошибку.

## Цель
Сделать каноническими helpers `paths.SessionsDir(a.profile)` и `paths.SessionDir` для session, rename и status watcher paths, сохранив `AI_DATA_HOME` и `ProfileSlug`.

## Файлы
- `main.go` — `sessionBaseDir` и прямые joins путей в rename/watcher.
- `main_rename.go` — joins каталогов session/familiar.
- `internal/status/status.go` — `statusPath` через `paths.SessionDir`, удаление локальных `dataHome`/`slugify`.
- `main_view_test.go` — проверки session/watcher paths.
- `main_rename_test.go` — проверки canonical rename/move paths.
- `internal/status/status_test.go` — проверки canonical status paths.
- `CONTEXT.md` — одна запись о проблеме и решении.
- Одна заметка в `AI/mistakes/` о проблеме и решении.

## Детали
- Использовать `paths.SessionsDir(a.profile)` для каталога sessions и `paths.SessionDir` для конкретной сессии во всех перечисленных runtime paths.
- Сохранить поведение `AI_DATA_HOME` и общий `ProfileSlug`, включая пробелы и кириллицу.
- `AUTOMATA_HOME` не изменять.
- Не выполнять миграцию, изменения UI, F5/F7 или TS consumers.
- **Approved scope:** только перечисленные файлы и канонизация session/status/rename/watcher paths.
- Спецификация одобрена; runtime- и test-реализация выполнены в указанном scope.

## Критерии приёмки
- [x] При заданных `AI_DATA_HOME` и отличающемся `HOME` используется `AI_DATA_HOME` — подтверждено `TestReadUsesCanonicalSessionPaths`, `TestAppSessionBaseDirUsesCanonicalPaths` и cuetty smoke.
- [x] Пустой профиль использует только `<base>/profiles/default/sessions/<id>`, а не `<base>/sessions/<id>` — подтверждено canonical tests и cuetty smoke.
- [x] Профили с пробелами и кириллицей используют тот же slug через `ProfileSlug` во всех путях — подтверждено кейсами `unicode profile slug` и путём `profiles/proekt-ω/sessions` в cuetty smoke.
- [x] Rename/move используют канонические session/familiar paths — подтверждено focused `TestRename*` и полным targeted набором.
- [x] Status readers и существующие watchers используют канонические paths — подтверждено `TestReadUsesCanonicalSessionPaths` и focused status/watcher cases.
- [x] Проходят `gofmt`, `go vet ./...`, `go test ./... -count=1 -p 1`, `go build -o automata .` и scoped `git diff --check` — все проверки завершились успешно.

## Результаты

Реализация и тесты используют `paths.SessionsDir`/`paths.SessionDir`; `AUTOMATA_HOME`, миграция, UI/F5/F7 и TS consumers не затрагивались. Свежий cuetty smoke завершён с маркером `CUETTY_CANONICAL_SESSION_PATH_SMOKE=PASS`.
