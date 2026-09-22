# Спецификация аудита Automata P0/P1/P2

**Статус:** утверждена Аней Келлер; реализация и проверка выполняются.

## 1. Цель

Закрыть подтверждённые дефекты в дереве, жизненном цикле PTY/сессий, миграции данных, fsnotify-наблюдателях, путях профиля/домена, запуске Pi и воспроизводимой проверке. Существующие пользовательские изменения, включая tracked-бинарник `automata`, не перезаписываются без сохранения и проверки.

## 2. Границы

### P0 — целостность и потеря работоспособности

1. **Циклы дерева.** `moveItem` и все клавиатурные/мышиные пути перемещения отклоняют перемещение элемента в себя или в любого потомка. При отказе дерево, выделение и `state.json` не меняются; рекурсивный обход не может уйти в бесконечность.
2. **Жизненный цикл процесса.** Эмулятор и сессия считаются активными только после успешного запуска/`PtyReadyMsg`. Ошибка запуска, остановка и `PtyExitMsg` очищают соответствующий cache и `activeSessions`; завершившийся эмулятор никогда не переиспользуется. Для familiar сохраняется передача exit-события в `ChatPanel`, чтобы вкладка удалилась штатно.
3. **Пути rename/move.** Перемещение между родителями меняет session ID/domain ID и переносит те же session directories, domain data, JSONL, familiars и Kanban assignments, что и rename. Используется существующая миграция из `main_rename.go`; ручной небезопасный `os.Rename` не дублируется. Проверка/миграция выполняются до фиксации изменения дерева либо имеют проверяемый rollback: ошибка не оставляет дерево и данные в разных состояниях. Перемещение наружу (`MoveSelectedOut`) проходит тот же путь.
4. **Lifecycle Bubble Tea.** Не добавлять собственные signal handlers и `os.Exit`/`log.Fatal` для завершения приложения. `Ctrl+C` и штатное завершение используют Bubble Tea (`tea.Quit`/возврат из `Run`); все PTY и fsnotify-ресурсы закрываются через явный lifecycle cleanup после `Run` и при замене владельца.

### P1 — устойчивость и корректность данных

5. **Наблюдатели.** `App.statusWatcher`, per-session watchers, `ContextPanel`/`KnowledgePanel` watchers закрываются перед заменой/удалением и при завершении приложения. На одного watcher существует не более одной блокирующей команды; re-arm не создаёт параллельных читателей и не теряет события. Закрытый watcher не re-arm-ится.
6. **Атомарное состояние.** `Tree.SaveState` сериализует snapshot и заменяет canonical `paths.StatePath(profile)` через временный файл в том же каталоге, `Sync`/`Close` и `Rename`; временные файлы удаляются при ошибке. Старый `state.json` остаётся валидным при сбое записи. Параллельные callbacks состояния не создают повреждённый результат.
7. **Canonical profile/domain/session paths.** Все UI, memory, context, jobs, status и migration paths используют `paths.ProfileSlug`, `paths.StatePath`, `paths.SessionDir` и `paths.DomainDir`. Не допускаются raw-профили, локальные `slugify` и вывод профиля из ID с другой нормализацией. Пустой профиль использует `default`; Unicode-профиль даёт тот же slug во всех пакетах. Domain ID для root и вложенных папок согласован с формированием session ID.
8. **Рекурсивный chat list.** `updateChatList(folder)` включает чаты на любой глубине внутри выбранной папки в стабильном порядке дерева, исключает папки и terminals и не смешивает элементы другого профиля.
9. **Запуск Pi.** `App.piLaunch` не привязан к существующему только на одной машине `/usr/local/bin/pi`: выбирает доступный `pi`/явный `PI_CMD`, сохраняет аргументы `--session-id`, `PI_CODING_AGENT_DIR` и `AUTOMATA_PROFILE` по единому контракту. `PI_CMD` не обрабатывается через shell-интерполяцию; отсутствие команды даёт явный отказ без подмены Pi обычным shell. Все launch paths (select, restore, task, clear, familiar) используют один resolver.

### P2 — воспроизводимость и контроль регрессий

10. **CI и воспроизводимая сборка.** Удалить зависимость `go.mod` от локальных `../../Starframe/...` путей. Внешние `portalis`, `cue-tty` и `warp` фиксируются immutable-версиями/commit-псевдоверсиями; чистый checkout не зависит от `/Users/a/Space`. CI выполняет форматирование, vet, тесты, сборку и `go mod verify`. Две сборки одинакового исходного checkout с `-trimpath -buildvcs=false` и одинаковым Go toolchain дают одинаковый SHA-256.
11. **Документация и бинарник.** После каждой ошибки обновляется `CONTEXT.md` строкой формата `[ДАТА] Проблема: X → Решение: Y`. Коммиты не создаются. После полной проверки бинарник собирается в проекте, его тип/hash проверяются, а уже запущенные Automata/zellij-процессы перезапускаются штатным способом; содержимое пользовательского tracked-бинарника до этого сохраняется.

## 3. Технические ограничения

- Не менять runtime-код до одобрения этой спецификации.
- Не возвращать закрытые пользователем вопросы и не расширять scope до folder-tabs, Dual workspace enforcement, mouse diagnostics или иных несвязанных задач.
- Не добавлять ручную обработку сигналов, `os.Exit`, shell-строки для запуска процессов или необратенное объединение каталогов.
- В тестах внешние FS/process/time-зависимости имеют injectable seams или изолируются `t.TempDir`; тесты не требуют реального Pi, сети, пользовательского HOME или живого процесса.
- Миграции должны быть проверяемыми при collision/missing source/failed write и не удалять target.
- Все новые/изменённые файлы заканчиваются переводом строки; комментарии в Go/TypeScript — только на английском.

## 4. Обязательные regression-тесты

Минимальный набор:

- `internal/tree`: self-move, move-into-descendant, move-out migration callback, отказ без autosave и сохранение acyclic invariant.
- `main`/lifecycle: start failure не активирует сессию; ready активирует ровно один раз; normal и familiar exit удаляют cache; следующий launch создаёт новый emulator; stop/clear/rename не оставляют stale IDs.
- `main_rename`/`paths`: cross-parent chat, terminal, folder subtree и `MoveSelectedOut` переносят все данные; collision/failure оставляют исходные данные; Unicode/default profile paths совпадают.
- status/knowledge/context watchers: close при смене session, close при удалении, close после app shutdown, один pending command на watcher и bounded re-arm без leak.
- state: успешная atomic replacement, ошибка temp/write/rename оставляет старый JSON читаемым и не оставляет мусор.
- domain/chat list: nested chats, root chat, Unicode profile и canonical domain/session IDs.
- Pi: `PI_CMD`, PATH-resolved `pi`, configured agent dir, отсутствующий executable, аргументы/окружение и отсутствие shell fallback.
- CI: чистый checkout без локальных replace-путей, `go mod verify`, две reproducible builds и обязательные quality commands.

## 5. Порядок реализации

1. После approval сохранить baseline `git status`, hash tracked-бинарника и существующие пользовательские изменения.
2. Реализовать P0 малыми изменениями; после каждого изменения запускать адресные тесты и `gofmt`.
3. Реализовать P1 и соответствующие regression-тесты.
4. Реализовать P2/CI и проверить чистый bootstrap.
5. Выполнить полный verification cycle, затем собрать бинарник и сделать manual cuTTY smoke: запуск, folder → `Content`, confirmation modal, `read_notes`, normal/familiar exit и `Ctrl+C`.

## 6. Критерии приёмки и команды

Работа считается завершённой только если одновременно выполнены все acceptance criteria выше и:

```bash
gofmt -l .                         # пустой вывод
go vet ./...                       # exit 0
go test ./... -count=1 -p 1        # exit 0
go build -trimpath -buildvcs=false -o automata .  # exit 0
go mod verify                      # exit 0
git diff --check                   # пустой вывод
```

Дополнительно CI должен выполнить чистый checkout/bootstrap и сравнение SHA-256 двух сборок. Для изменённых lifecycle/watcher-тестов запускаются адресные `go test` с `-count=1`; ручной smoke использует только свежесобранный бинарник. Коммит не создаётся.

**Вопрос для approval:** «Утверждаем эту спецификацию без расширения границ?»
