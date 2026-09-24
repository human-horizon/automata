# Corrective hardening main от 2026-09-24

## Контекст
После hardening в `main@c724101e38c88568e14f844d1a40d36493a9e739` аудит выявил flaky/неверную геометрию collapse E2E и residual случаи, способные запускать несуществующие сессии, пропускать malformed job metadata при destructive preflight, смешивать legacy-профили или принимать чужие familiar session ID. Ещё несколько UI-диагностик существуют только в логах или используют координаты, не совпадающие с отрисовкой.

## Цель
Закрыть перечисленные P0/P1/P2 сценарии непосредственно в `main`, не ослабляя уже смёрженные state, lifecycle, profile, persistence и migration инварианты. После изменений локальная проверка и CI на опубликованном `main` должны быть зелёными.

## Что изменится
- `e2e/collapse_test.go` — измерять левый край чата и границу Knowledge по реальному экрану до/после collapse, проверять фактическое расширение без предположения о ширине Tree.
- `e2e/chat_plan_test.go` — находить toolbar `+` и пункт `+ Chat` в фактических terminal lines, не привязываясь к устаревшим координатам при длинном profile title.
- `internal/tree/render.go` и тесты — обрезать длинный profile title, сохраняя видимыми и доступными root-menu и `+` toolbar buttons в узкой панели.
- `internal/tree/state.go`, `main.go` и тесты — фильтровать persisted `active_sessions` по валидным session ID загруженного Tree; неизвестные записи отбрасывать с наблюдаемым предупреждением, не отвергая здоровый Tree и не создавая emulator.
- `internal/ai-knowledge/jobs/reader.go` и тесты — fail-closed `PrepareKillSessionForProfile` для существующего job directory с unreadable/malformed/неконсистентной metadata; до успешного полного preflight никаких сигналов или runtime mutation.
- `internal/tree/migrate.go` и тесты — ограничить `LoadState(profile)` миграцией только default legacy source для default profile или только legacy-папки, чей canonical slug совпадает с запрошенным профилем.
- `internal/paths`, `internal/ui/chat_panel.go`, `main.go` и тесты — валидировать familiar registry `sessionId` как безопасный идентификатор с префиксом `<owner>__` и непустым suffix; сохранить текущие dotted/profile session ID форматы и уже существующие tabs при ошибке.
- externally-fed destructive session/job wrappers и тесты — применять общую валидацию session ID до построения filesystem paths; не менять без необходимости низкоуровневый `SessionDir` контракт.
- `internal/ui/knowledge_panel.go` и тесты — вычислять позиции кнопок Auto/Dual одним helper-ом для рендера и hit-testing, используя терминальную ширину.
- `internal/ui/kanban_panel.go` и тесты — показывать предупреждение при частичном чтении задач, сохраняя валидные карточки.
- `main.go`, `main_rename.go` и UI/tests — показывать post-commit cleanup warning после успешного rename/move без rollback committed операции.
- `CONTEXT.md` — зафиксировать реализованные решения и любые встреченные ошибки в формате проекта.

## Детали реализации
1. Сначала измерить исходное collapse E2E на свежем временном бинарнике. В тесте находить реальный левый край уникального chat content и реальную границу `│Knowledge` до и после клика; ширину вычислять из этих координат и терминальных ячеек. Дожидаться стабилизации вложенного resize; проверять смещение chat left edge и использование не менее 80% фактически освобождённых ячеек, не используя константу collapsed Tree width.
2. После `fromStateItems` собрать session keys всех листьев с учётом профиля; оставить только совпадающие `ActiveSessions`. Unknown IDs удалить из candidate snapshot, зарегистрировать warning через существующий диагностический канал и позволить последующему SaveState записать очищенное состояние. `restoreSessions` дополнительно проверяет наличие Tree item до обращения к emulator factory; общий emulator creator сохраняет поддержку легитимного Clear/restart для сессий вне Tree.
3. В destructive job preflight отличать отсутствие `jobs/` от malformed metadata. Для каждого job directory ошибка чтения/декодирования, пустой/несовпадающий ID, невалидные PID/StartedAt или неизвестная process identity возвращают ошибку; не-running record пропускается только после успешной структурной проверки. До завершения preflight `processSignal` не вызывается.
4. `migrateLegacyData(profile)` не обходит все legacy profile directories. Пустой profile обрабатывает только корень `~/.automata`; именованный profile находит только директорию с совпадающим `slug.Slug(entry.Name())`. Ошибки чужих legacy-профилей не влияют на загрузку текущего профиля.
5. Валидатор session ID запрещает пустое значение, разделители путей, traversal, NUL/control characters, абсолютные/drive paths; знакомые точки в сгенерированных dotted IDs разрешены. Familiar entry проходит только если `sessionId` безопасен, начинается с `ownerSessionID + "__"` и имеет непустой безопасный suffix, отличающийся от другого session. При ошибке `ChatPanel` сохраняет прежнее состояние вкладок и показывает диагностику.
6. Валидация применяется до filesystem resolution в familiar registry и externally-fed destructive job/session APIs. Existing profile ambiguity checks, PID identity checks и transactional lifecycle остаются обязательными.
7. `knowledgeHeaderLayout` (или эквивалент) вычисляет display-cell интервалы title/Auto/separator/Dual. И render, и mouse handler используют тот же результат; tests проверяют крайние клетки кнопок и separator.
8. Kanban panel сохраняет валидные задачи при `ReadAll` error и добавляет видимое, theme-aware предупреждение, что часть задач не загружена.
9. Ошибки `finalizeRenamePlan` после commit остаются предупреждениями, отображаемыми пользователю; исход rename/move не откатывается. Учесть оба callback-пути и прямой rename/move path.

## Критерии приёмки
- [x] Collapse E2E измеряет фактическую terminal-cell геометрию, не содержит предположения `collapsed width == 1`, дожидается стабилизации inner split, проверяет использование ≥80% реально освобождённой ширины и проходит 5 повторов на свежем `AUTOMATA_BIN`.
- [x] Chat/Plans E2E ищет toolbar `+` и пункт `+ Chat` по отрисованным terminal lines, а не фиксированным координатам, и стабильно открывает форму создания чата.
- [x] При длинном profile title Tree header ограничивает заголовок реальной шириной панели и сохраняет видимые/работающие toolbar buttons.
- [x] Неизвестный persisted active session ID удаляется без отказа загрузки здорового Tree; canonical ID именованного профиля сохраняется; ghost emulator не создаётся и не запускается.
- [x] Любая malformed/нечитаемая/неидентифицируемая job metadata в destructive preflight возвращает ошибку до process signal и runtime mutation; валидные не-running jobs допускают skip.
- [x] Повреждение unrelated legacy profile не блокирует миграцию и запуск текущего профиля; запрос повреждённого профиля возвращает ошибку.
- [x] Familiar registry отклоняет чужие, совпадающие с owner и path-like session IDs; валидные `<owner>__<suffix>` допускаются; ошибка не удаляет текущие tabs и видима пользователю.
- [x] Externally-fed destructive session/job API валидирует ID до обращения к filesystem.
- [x] Hitboxes Auto/Dual совпадают с terminal-cell layout; клики по первой/последней клетке кнопки действуют только на неё, separator не меняет настройки.
- [x] Частичная ошибка Kanban видна в UI, а прочитанные задачи остаются показанными.
- [x] Ошибка post-commit rename/move cleanup видна пользователю; committed operation остаётся committed.
- [x] Ранее существующие canonical identity, fail-safe state, transactional rollback/commit, familiar cleanup, atomic settings/jobs, profile isolation, KillPlan/PID safety, legacy ambiguity, Kanban YAML preservation и watcher recovery сохранены.
- [x] `gofmt -l .`, `go vet ./...`, `go mod verify`, указанные адресные пакеты, collapse E2E ×5, полный `go test ./... -count=1 -p 1`, `git diff --check` и две совпадающие reproducible build hashes успешны.
- [ ] После коммита рабочее дерево чистое.
- [ ] Изменения закоммичены прямо в `main`, push отправлен в `origin/main`; local HEAD и remote `main` SHA совпадают, GitHub Actions на этом SHA завершился `success`.

## Границы
Работа только в `main`; без feature/corrective веток и PR. Не заменять зафиксированный tracked `automata` бинарник; для E2E использовать свежий временный binary через `AUTOMATA_BIN`. Не ослаблять ранее смёрженные hardening инварианты. Любое расширение scope требует отдельного согласования.
