# Финальное укрепление Automata и правой панели

## Контекст

Аудит `main` подтвердил оставшиеся проблемы в проверке `--profile`/`--pi`, ошибках Clear и назначении Kanban-задач, обновлении списка чатов, пользовательской диагностике, атомарной записи и fallback-путях `/Users/a`. Читатели status/plans/jobs частично скрывают повреждённые записи. Правая Knowledge-панель уже поддерживает прокрутку колесом, стрелками и Page Up/Down, но показывает заголовок `Knowledge`, не позволяет сворачивать Plans/Jobs и переносит длинные строки без висячего отступа.

## Цель

Закрыть подтверждённые lifecycle/data-integrity дефекты без изменения владельца и retention политики `jobs/<id>`. Сделать правую панель компактной: убрать её заголовок, сворачивать Plans/Jobs и выравнивать продолжения строк по тексту элемента.

## Границы

- Работа выполняется в `main`; commit и push не выполняются.
- Не менять ownership, retention и удаление истории `jobs/<id>`; это отложено до отдельного аудита Pi producer.
- Не заменять tracked executable `automata`; E2E использует временный бинарник через `AUTOMATA_BIN`.
- Сохранить существующие Tree/runtime/profile/persistence/migration/familiar/Kanban invariants.
- Уже существующая прокрутка Knowledge-панели сохраняется: колесо, Up/Down, Page Up/Down. Видимый scrollbar не добавлять без отдельного требования.

## Что изменится

1. `main.go`, `main_lifecycle.go` и root tests — ранняя проверка profile/`--pi`, отсутствие запуска task worker для отсутствующего/не-chat Tree item, отсутствие дублирующего PTY, Clear с fail-closed restart и видимыми агрегированными ошибками, lifecycle warnings.
2. `internal/paths` и профильные tests — убрать machine-specific home fallback, безопасно получать текущий home и ограничить `--pi` одним корректным tag-сегментом.
3. `internal/tree` и tests — уведомлять приложение об успешных изменениях состава/порядка/identity элементов, чтобы Kanban picker синхронизировался после commit Tree mutation.
4. `internal/kanban`, `internal/ui/kanban_panel.go`, `internal/ui/context_panel.go` и tests — collision-safe создание задач; транзакционные assignment/remove/reassign/delete с rollback task/status snapshots; сообщения об ошибках в видимой области.
5. `internal/atomicfile` (новый общий helper), Tree/Kanban/settings/job metadata/profile registry/session JSONL/memory/migration writers и tests — общий streaming-capable temp/write/sync/close/rename/directory-sync контракт с явной ошибкой «commit уже состоялся».
6. `internal/ai-knowledge/context`, `internal/ai-knowledge/jobs`, `internal/ai-knowledge/ui`, `internal/ui/container.go`, `internal/ui/knowledge_panel.go` и tests — сохранить валидные частичные данные и отображать диагностики чтения/декодирования.
7. `main.go`, mouse tests, `e2e/collapse_test.go` и E2E — F8 включает/выключает все активные mouse modes, в том числе 1003; collapse geometry измеряется новым правым header control после удаления заголовка.
8. `internal/ui/familiar_panel.go`, его tests и устаревшие маршруты `internal/ui/chat_panel.go` — после production grep подтвердилось отсутствие создания старого tmux FamiliarPanel; legacy panel, tests и мёртвые маршруты удалены, современные familiar TermPanel flows сохранены.
9. `internal/ai-knowledge/ui/render.go`, `internal/ui/container.go`, правая Knowledge-панель и tests — убрать слово `Knowledge` из строки заголовка, сворачивать Plans/Jobs по клику на видимый заголовок, передавать панели полный набор строк для сохранения прокрутки, пересчитывать вложенный split при изменении viewport width и выравнивать переносы по тексту после checkbox/bullet.
10. `CONTEXT.md` — фиксировать выявленные ошибки и решения в формате `[ДАТА] Проблема: X → Решение: Y`.

## Детали реализации

### Пути и запуск

- Пустой `--profile` означает canonical default; непустое имя принимается, только если `paths.ProfileSlug` не пуст. Проверка выполняется до профилирования, лог-файла и создания App.
- Пустой `--pi` отключает привязку к каталогу Pi. Иначе tag принимается только как один непустой path segment из букв/цифр/`_`/`-`; `.`/`..`, разделители и управляющие символы отвергаются до I/O.
- Текущий home определяется средствами ОС, без literal `/Users/a`; если home необходим и получить его нельзя, запуск завершается диагностикой до файловых операций. `AI_DATA_HOME` продолжает переопределять каталог Automata.

### Lifecycle и Kanban

- `startAssignedTaskSession` проверяет канонический session ID и соответствие существующему chat item в Tree до создания Pi emulator. Уже активный emulator не запускается повторно.
- Ошибка до фактического PTY start откатывает assignment и status snapshot. После старта worker остаётся запущенным при ошибке сохранения active-session snapshot; ошибка показывается как committed warning и не вызывает rollback/остановку worker.
- Clear сначала отказывает при непрошедшем job preflight. После committed остановки выполняет доступные обязательные очистки, собирает все ошибки и не запускает Pi заново, если очистка не завершилась. Отсутствующие optional JSONL не считаются ошибкой; ошибки чтения/удаления/очистки registry видны пользователю.
- Создание task использует эксклюзивное создание имени и не затирает существующий файл. Ошибки create/update/delete/assignment показываются в Kanban-панели.
- Assignment, снятие, переназначение и удаление задачи сохраняют исходные Kanban/status snapshots и восстанавливают их при ошибке до commit. Ошибки rollback также включаются в диагностику.
- После committed Tree create/rename/move/delete/reorder приложение пересобирает список доступных чатов в текущей области выбора. В список попадают только chat items; порядок соответствует дереву.

### Атомарная запись и частичные данные

- Общий writer создаёт временный файл в том же каталоге, задаёт mode, записывает, sync/close, rename и sync каталога. Ошибка до rename означает отсутствие commit; ошибка после rename явно обозначается как committed.
- Перевести затронутые Tree/Kanban/settings/job metadata/profile registry writers на общий контракт, сохраняя их прежние режимы/данные и caller-specific rollback semantics. Не включать сюда удаление/перенос `jobs/<id>`.
- Context reader возвращает валидные данные вместе с агрегированной диагностикой повреждённых файлов/элементов. Jobs reader возвращает валидные jobs вместе с path-specific ошибками повреждённых/нечитаемых записей. Knowledge-панель показывает предупреждение, не скрывая частичные данные.
- Удалить FamiliarPanel и связанные сообщения/маршрутизацию только после поиска всех production references: существующие Familiars используют Portalis TermPanel.

### Правая панель и мышь

- Строка панели больше не содержит `Knowledge`; кнопки auto/dual и их hitboxes остаются рабочими.
- Plans и Jobs изначально раскрыты. Левый клик по видимому заголовку переключает соответствующую секцию; состояние хранится только в памяти панели, без записи в настройки.
- Прокрутка работает по колесу мыши, Up/Down и Page Up/Down; offset ограничивается актуальной высотой после сворачивания/изменения ширины.
- В иерархии сохраняются отступы раздел → план → шаг/задача. Продолжения длинного шага/команды выравниваются по началу текста после checkbox/bullet marker, а не по левому краю панели. Ширина проверяется в terminal cells, включая Unicode.
- F8 включает и выключает согласованный набор режимов 1000/1002/1003/1006; `tea.WithMouseAllMotion` и ручные переключения не оставляют all-motion включённым в режиме выбора текста.

## Критерии приёмки

- [x] Невалидные profile и `--pi` tag отвергаются до I/O; нормальные Unicode profile и обычные Pi tags сохраняют canonical paths.
- [x] Ни один static `/Users/a` fallback не остаётся в production path resolution.
- [x] Clear агрегирует cleanup errors, не перезапускает сессию после обязательной ошибки и показывает причину; успешный Clear по-прежнему заменяет panel emulator.
- [x] Отсутствующий/non-chat Tree item не запускает task worker; активный worker не дублируется; pre-commit start failure откатывает task/status; post-commit persistence warning оставляет worker запущенным.
- [x] Chat picker отражает последние committed Tree изменения, без папок, terminals и удалённых/перемещённых из текущей области чатов.
- [x] Task creation не затирает коллизию; assignment/remove/reassign/delete ошибки сохраняют или восстанавливают task/status snapshots и видимы в UI.
- [x] Atomic writer различает pre-commit и post-commit ошибки; прежние state/settings/task/job данные остаются валидными при ошибке до rename.
- [x] Context/jobs partial-read regressions подтверждают, что валидные entries остаются видимыми, а ошибочные диагностируются.
- [x] Production grep подтверждает отсутствие создания FamiliarPanel до его удаления; остались только современные familiar TermPanel flows.
- [x] F8 регрессии подтверждают включение/выключение mode 1003 вместе с прочими используемыми режимами.
- [x] Knowledge-заголовок не отображает `Knowledge`; Plans/Jobs сворачиваются/раскрываются кликом; scroll и hitboxes сохраняются; переносы выровнены по тексту, не выходят за ширину; collapse E2E подтверждает минимум 80% роста Chat на освобождённую ширину.
- [x] Targeted tests проходят после каждой фазы; затем проходят gofmt, go vet, go mod verify, `go test -race ./internal/... -count=1 -p 1`, полный `go test ./... -count=1 -p 1`, E2E с временным `AUTOMATA_BIN`, две совпадающие reproducible builds и `git diff --check`.
- [x] Изменения остаются незакоммиченными в `main`; tracked executable и посторонние файлы не изменены.
