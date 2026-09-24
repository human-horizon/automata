# Automata: третий проход hardening — identity и целостность состояния

## Контекст

В дереве нет общего барьера уникальности session/domain slug: создание напрямую добавляет элементы, а перемещение не проверяет целевого родителя. `LoadState` проглатывает ошибки и может заменить повреждённое состояние пустым деревом; `newApp` игнорирует результат загрузки. Некоторые структурные изменения откатываются при ошибке сохранения, другие лишь логируют её. Панель Knowledge при переключении настроек может перезаписать некорректный `settings.json` и потерять неизвестные ключи. Ошибка чтения `familiars.json` трактуется как пустой список, а внешнее удаление фамильяра закрывает только вкладку. Job JSON записывается напрямую, ошибки диагностики Kanban-файлов теряются, а попадание мышью по вкладкам считает байты вместо экранных ячеек.

## Цель

Закрепить уникальность canonical sibling identity при создании, перемещении и загрузке; сохранять Tree и связанные данные без тихой потери при ошибках. Исправить integrity settings/familiar/job/Kanban и Unicode hit-testing, сохранив предыдущие profile, lifecycle, migration и CI-инварианты.

## Что изменится

1. `internal/tree/model.go`, `internal/tree/state.go`, `internal/tree/input.go`, `internal/tree/render.go` — общая identity-валидация, fail-safe state, transactional mutations и видимые ошибки.
2. `main.go`, `main_lifecycle.go`, `main_delete_test.go`, `main_lifecycle_test.go` и root tests — startup diagnostics, согласование удаления с runtime lifecycle и regressions.
3. `internal/ui/chat_panel.go`, `knowledge_panel.go`, `kanban_panel.go` и соответствующие tests — безопасная работа с registry/settings/Kanban errors и hit-testing.
4. `internal/ai-knowledge/jobs/reader.go` и tests — атомарное обновление job metadata и обработка ошибок.
5. `internal/kanban/kanban.go` и tests — диагностика некорректных файлов без молчаливого пропуска.
6. `specs/third-hardening-identity-state-integrity.md`, `CONTEXT.md` — принятая спецификация, результаты проверок и записи `[ДАТА] Проблема: X → Решение: Y`.

## Детали реализации

### 1. Canonical sibling identity

- Общий ключ имени: `slug.Slug(strings.TrimSpace(name))`. Пустой ключ недопустим.
- Уникальность проверяется среди всех соседей независимо от типа элемента. Например, имена `Foo Bar`, `foo-bar` и `FOO_BAR` конфликтуют.
- Проверка обязательна для всех root/child Create API, rename, cross-parent Move/drag-and-drop и `MoveSelectedOut`; она выполняется до callback-ов миграции, изменения дерева и иных side effects. Перестановка внутри одного parent разрешена и не вызывает миграцию.
- Checked API возвращают ошибку; существующие цепочные `Add*` wrappers сохраняют совместимость, но при ошибке ничего не меняют. UI показывает ошибку создания/переименования в текущем modal, прочие ошибки — в видимом сообщении Tree.
- `Item.ID` остаётся runtime UUID и не становится canonical identity: session/domain identity по-прежнему строится из slug-компонентов пути.

### 2. Fail-safe Tree state

- Текущий формат продолжает сохраняться как version `2`; версия отсутствует/`0`, `1` и `2` читаются. Неизвестная будущая версия отклоняется.
- Весь JSON читается и валидируется как кандидат до изменения Tree. Отклоняются повреждённый JSON, nil-элементы, пустые canonical names, коллизии siblings и неоднозначная форма узла (например, folder+terminal или дети у leaf). При ошибке текущие in-memory данные не меняются.
- Отсутствующий файл остаётся штатным пустым состоянием. Ошибка чтения, валидации или миграции возвращается и показывается при запуске; невалидный snapshot не перезаписывается последующими autosave до успешной загрузки валидного состояния. Существующая resumable migration сохраняет прежнюю семантику.
- Запись остаётся atomic: временный файл в том же каталоге, sync, rename, sync каталога. Ошибка до rename означает неприменённую запись. Ошибка sync каталога после rename возвращается как явно committed warning: вызывающий код сохраняет согласованное новое in-memory/runtime состояние и выполняет post-commit callbacks, не делает ложный rollback.

### 3. Transactional structural mutations

- К транзакционным изменениям относятся Create/Delete/Rename/Move/MoveSelectedOut, порядок siblings (включая sort), archive и bind/unbind.
- При ошибке до durable commit восстанавливаются прежние slices/поля/parent links/selection и откатываются подготовленные внешние миграции; старый atomic state file не переписывается повторным `SaveState`.
- После committed rename/move выполняются их существующие completion callbacks. Ошибка post-rename directory sync показывается как warning, но не запускает rollback уже сохранённого state и внешних данных.
- Delete разделяется на безопасный preflight и commit: до сохранения Tree можно подготовить/проверить `KillPlan`, но нельзя останавливать процесс, менять cache/watcher/runtime maps или active sessions. Ошибка preflight/save оставляет дерево и runtime нетронутыми. После committed удаления выполняется runtime cleanup; его committed warning показывается и не восстанавливает уже удалённый item. Fail-closed проверки PID и aggregate job cleanup сохраняются.
- Нефункциональные UI/layout изменения (например, выбор темы/ширины или раскрытие папки) не расширяют транзакционный scope; их прежнее поведение не ухудшается.

### 4. Knowledge settings

- Загрузка и запись различают отсутствующий файл и ошибки чтения/JSON/schema. Повреждённый файл не считается пустым объектом.
- Обновление `autoContinue`/`dual` строится поверх `map[string]json.RawMessage`, сохраняет неизвестные ключи, пишет атомарно через temp+sync+rename+directory sync.
- Ошибка записи не меняет показанные значения переключателей и не портит прежний файл; причина доступна в UI и логах. Успешное переключение сохраняет взаимное исключение `autoContinue`/`dual`.

### 5. Familiar registry и lifecycle

- Отсутствующий `familiars.json` — валидный пустой registry. Ошибка I/O, JSON или структуры — диагностика, без изменения `known`, вкладок или runtime; повторная проверка остаётся на следующем poll.
- При подтверждённом внешнем удалении вызывается host cleanup для PTY/emulator cache, running/active sessions, watcher и jobs через существующий fail-closed lifecycle. Эта ветка не удаляет запись из уже изменённого registry и не удаляет JSONL.
- Ошибка preflight оставляет вкладку и familiar tracking для повторной попытки. После committed cleanup вкладка удаляется даже при committed warning; warning остаётся видимым.

### 6. Job metadata и Kanban diagnostics

- `writeJSON` для job metadata заменяет файл атомарно; ошибки больше не игнорируются. Stale-job directory удаляется только после успешной записи `exited` metadata; иначе исходный job record сохраняется и вызывающий получает ошибку.
- `kanban.ReadAll` возвращает все успешно прочитанные задачи и объединённую path-specific ошибку по пропущенным повреждённым `.md` файлам. UI сохраняет отображение валидных задач и логирует диагностику; rename/move миграция не коммитится, если входная доска прочитана не полностью.

### 7. Unicode hit-testing

- Координаты мыши считаются в терминальных ячейках (`lipgloss.Width`/ANSI-aware width), не по `len(string)` или byte offset.
- Hit regions ChatPanel совпадают с отрисованной шириной имён и кнопки `×`, включая CJK/широкие Unicode и combining marks; обрезанные/невидимые вкладки не получают hit target.
- Tree action-icon matching сохраняет визуальную, ANSI-aware семантику; добавляются regressions на wide/combining имена.

## Критерии приёмки

- [x] Create/Rename/Move/MoveSelectedOut отклоняют canonical sibling collisions всех типов без частичных изменений и до внешних callbacks.
- [x] LoadState валидирует версии 0/1/2 и всю иерархию до commit; invalid/future state сохраняет прежний Tree и файл, а startup не игнорирует ошибку.
- [x] Для каждого structural mutation тест подтверждает rollback при ошибке до commit; post-rename commit warning не вызывает откат сохранённого state.
- [x] Delete save/preflight failure не меняет runtime; committed cleanup очищает familiar/jobs/watchers и оставляет наблюдаемую ошибку при частичном warning.
- [x] Knowledge settings сохраняют неизвестные вложенные ключи; invalid JSON и injected atomic-write failure не портят файл и не меняют toggles.
- [x] Ошибка/повреждение familiar registry не закрывает tabs; external removal останавливает runtime/jobs без повторного удаления registry/JSONL и retry-ит preflight failure.
- [x] Ошибка job metadata write сохраняет предыдущий валидный JSON и не удаляет единственную job directory-копию.
- [x] `kanban.ReadAll` возвращает валидные записи вместе с диагностикой path-ов невалидных файлов; callers обрабатывают partial result явно.
- [x] Mouse regressions проходят для wide/combining Unicode имён табов и Tree action icons.
- [x] Ранее закрытые profile isolation/default, fail-closed PID/KillPlan, migration resume, metadata-preserving writes, watcher recovery, Kanban nested YAML/status и E2E binary/artifact invariants не регрессируют.
- [x] Проходят targeted Go tests, `gofmt -l .` (пустой вывод), `go vet ./...`, `go mod verify`, `git diff --check` и CI-style `AUTOMATA_BIN=<temporary binary> go test ./... -count=1 -p 1` с временными artifacts.
- [x] Две сборки `go build -trimpath -buildvcs=false` в разных временных каталогах дают одинаковый SHA-256; tracked executable `automata` не перезаписывается.
- [ ] Новый PR открыт в `main` от базы `fad00381917fecc50689854bc6cfe4c9877a3554`; remote HEAD совпадает с проверенным PR HEAD, GitHub CI зелёный. PR остаётся открытым — merge только по отдельному запросу.

Последний критерий оставлен незакрытым: изменения сохранены локально и незакоммичены; самостоятельно создавать commit запрещено. Новый PR не создавался, а локальный результат не выдаётся за опубликованный PR.

## Границы

Не менять Pi core, `node_modules`, `/Users/a/.ai/just/pi` или tracked executable `automata`. Не ослаблять profile/process identity, lifecycle, migration и Kanban metadata invariants. Production-процессы не перезапускать. Commit/push/PR выполнять только в рамках явно одобренного scope; PR не мёржить без отдельного указания.
