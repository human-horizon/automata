# Полный переход правой панели на fsnotify

## Контекст

Правая панель Automata показывает статус, планы, jobs и заметки текущего чата. Сейчас данные обновляются через `RefreshKnowledgeCmd`, который запускается по 5-секундному таймеру в `main.go`, и через `Refresh()` на `KnowledgePanel` (тоже дёргается из того же тика). После запуска, остановки и истечения фоновой job пользователь видит устаревший список до 5 секунд, а нередко и пустой — кеш `jobs.CachedReader` не учитывает вложенные `job.json`, и `pidIsSameProcess` может ошибочно принять живой процесс за мёртвый при сбое `ps`. KanbanPanel уже подтвердил работающий паттерн с fsnotify.

## Цель

Вся правая панель должна реагировать на изменения в файлах мгновенно через `fsnotify`, без периодического тика, и показывать список jobs корректно, пока процессы живут.

## Что изменится

1. `internal/ui/knowledge_panel.go` — отдельный watcher на каталог `jobs/`, на `status.json`, `plans.json`, `notes.json` текущего чата; реакция `jobsChangedMsg` и `knowledgeChangedMsg` в `Update`; выпиливается периодический `Refresh()` для этих данных.
2. `internal/ui/kanban_panel.go` — при необходимости выпиливается fallback `kanbanTickMsg` (Аня подтвердила, что fallback не нужен), остаётся только fsnotify Cmd.
3. `main.go` — убрать вызов `RefreshKnowledgeCmd` из тика; вместо него реагировать только на `KnowledgeRefreshMsg` от watcher-ов; сохранить `refreshTreeStatusBadges` отдельно, если он ещё нужен для дерева.
4. `internal/ai-knowledge/jobs/reader.go` — оставить `CachedReader` с сигнатурой, добавить экспортный `ListAll` или аналог, чтобы панель могла получить полный список с учётом живых PID.
5. `internal/ai-knowledge/context/reader.go` — добавить watcher на `plans.json`/`status.json`/`notes.json` (если отсутствует).
6. Тесты: `internal/ui/knowledge_panel_test.go`, расширение `kanban_panel_test.go` (без fallback), при необходимости — `internal/ai-knowledge/jobs/reader_test.go`.
7. `CONTEXT.md` — запись найденной проблемы и решения.

## Детали реализации

1. Добавить в `KnowledgePanel` поля `watchers []*fsnotify.Watcher`, `watchPaths []string`, `watchPending map[string]bool`.
2. `setupWatchers()` создаёт watcher на родительский каталог сессии (один watcher покрывает и `status.json`, и `plans.json`, и `notes.json`, и `jobs/`). Если каталог ещё не создан — отложить до первого успешного `Refresh()`.
3. Перенести закрытие watcher-а из `closeWatcher()` в `SetSession()` и при размонтировании.
4. В `Update` добавить обработчики `jobsChangedMsg` и `knowledgeChangedMsg`:
   - `jobsChangedMsg` → `Refresh()` только секции jobs, обновить `k.jobs` через `k.jobsReader.List(k.sessionID)`, установить `k.lastRefresh`.
   - `knowledgeChangedMsg` → перечитать `context.CachedReader` и `notes`.
5. `watchKnowledgeCmd()` и `watchJobsCmd()` блокируются на своих `Events` каналах и возвращают соответствующее сообщение; после `Update` они автоматически пере-экспортируются, как в `watchKanbanCmd`.
6. Удалить из `main.go` 5-секундный таймер `RefreshKnowledgeCmd`, оставить только `refreshTreeStatusBadges`. Сейчас он в строке `return a, tea.Batch(a.container.RefreshKnowledgeCmd(), a.blinkCmd())` (найдено через `grep`).
7. `KanbanPanel`: убрать `kanbanTickMsg`, `tickPending`, `tickCmd()`. Оставить только `watchKanbanCmd` и `kanbanChangedMsg`. Скорректировать тесты `TestFallbackIntervalIs5s` и связанные — заменить на проверку, что fallback не используется.
8. В `jobs/reader.go` оставить текущий корректный `List` с `kill(0)` и fallback-сигнатурой для вложенных файлов.
9. Не нарушать поведение остальных panels (chat, terminal) и существующих хуков.
10. После удаления тика убедиться, что `KnowledgePanel.Update` возвращает нужный Cmd, чтобы `Update` родительского `App` запускал blocking watcher.

## Критерии приёмки

- [ ] Правая панель обновляется сразу при изменении `status.json`, `plans.json`, `notes.json`, появлении/удалении `jobs/<id>/job.json` без перезапуска Automata.
- [ ] Никакого периодического опроса знаний не остаётся: только fsnotify.
- [ ] KanbanPanel продолжает работать без fallback-тика.
- [ ] Живая job в списке отображается, пока процесс существует, даже при сбое `ps`.
- [ ] После `kill` процесса job пропадает в течение секунды.
- [ ] `gofmt`, `go vet ./...`, `go test ./... -count=1` зелёные, включая e2e.
- [ ] Бинарник Automata пересобран.
- [ ] Существующие `job_list` (tool pi) и очистка продолжают работать.
