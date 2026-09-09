# Event-driven бейджи статуса в дереве

## Контекст

После `watch-jobs-panel` (2026-07-31) правая панель Automata (KnowledgePanel) стала реактивной через fsnotify на `status.json`/`plans.json`/`settings.json`/`jobs/`. Аня подтвердила, что периодический fallback не нужен.

Дерево чатов (Tree, левая панель) использует тот же `status.json` для отображения emoji-бейджа (`● thinking`, `○ idle` и т.п.) и обновлялось из 5-секундного `RefreshKnowledgeCmd`-таска. В ходе `watch-jobs-panel` этот таск был удалён, но `refreshTreeStatusBadges` (определена в `main.go:675`) осталась **мёртвым кодом** — её никто не вызывает. Симптом: левая панель застряла на последнем показанном бейдже (чаще всего fallback `○ idle`), правая при этом показывает корректный `● thinking` (или что-то ещё) — потому что KnowledgePanel реактивна, а Tree нет.

## Цель

Сделать бейджи Tree реактивными через fsnotify на каталоге сессий, как KnowledgePanel. Никаких periodic-тиков. Функция `refreshTreeStatusBadges` становится реактивным обработчиком события.

## Что изменится

1. `main.go` — добавить watcher-ы на `sessionBaseDir()` (родитель) и на каждый существующий подкаталог `<id>/`; blocking Cmd `watchTreeStatusCmd()`; обработка `treeStatusChangedMsg` в `Update`; вызов `refreshTreeStatusBadges` в `Init` и на каждом событии; корректное закрытие watcher-ов при выходе.
2. `main_view_test.go` — тесты: watcher создаётся в Init, событие триггерит пересчёт бейджей, удаление watcher-а при отсутствии каталога сессий не падает.
3. `CONTEXT.md` — запись проблемы/решения.

## Детали реализации

### Состояние App

```go
type App struct {
    // ... существующие поля
    statusWatcher      *fsnotify.Watcher         // на sessionBaseDir()
    sessionWatchers    map[string]*fsnotify.Watcher // sessionID -> watcher на <id>/
    statusWatchPending bool
}
```

### Пути

- `sessionBaseDir()` — `~/.ai/automata[/profiles/<slug>]/sessions/`
- Каждая сессия — `<sessionBaseDir>/<id>/status.json`
- Имя сессии в URL-кодированном slug, не точка/не слэш — безопасно для имени каталога

### Жизненный цикл

1. `Init()`:
   - `os.MkdirAll(sessionBaseDir(), 0755)` чтобы каталог существовал
   - `setupStatusWatcher()` — создаёт `statusWatcher` на `sessionBaseDir()`, для каждого существующего `<id>/` — `sessionWatchers[id]`
   - `refreshTreeStatusBadges(time.Time{})` — первичный расчёт бейджей
   - возвращает `tea.Batch(..., a.watchTreeStatusCmd())`

2. `setupStatusWatcher()`:
   - Создаёт `fsnotify.NewWatcher()`, `Add(sessionBaseDir())`
   - Для каждого `<id>/` (не-архив, не-терминал — `os.ReadDir` + фильтр по наличию `status.json` не обязателен, можно на все): `Add(filepath.Join(sessionBaseDir(), id))`
   - На ошибку (например, профиль не создан) — логируем, не паникуем. UI просто не покажет бейджей, как сейчас.

3. `watchTreeStatusCmd()`:
   - Если `statusWatcher == nil` — `nil`
   - `func() tea.Msg { _, ok := <-statusWatcher.Events; if !ok { return nil }; return treeStatusChangedMsg{} }`
   - В `Update` после обработки `treeStatusChangedMsg` — re-arm

4. `Update` обработка `treeStatusChangedMsg`:
   - Сбросить `statusWatchPending = false`
   - Применить pending-изменения: для каждого `CREATE` каталога в `sessionBaseDir` (через буфер `statusWatcher.Events` или чтение списка) — `attachSessionWatcher(id)`. Для каждого `REMOVE` — `detachSessionWatcher(id)` и `delete(statusBadges, id)`. Для `WRITE/CHMOD` status.json — `refreshTreeStatusBadges`.
   - Re-arm `watchTreeStatusCmd()`.
   - Вернуть cmd (бандл с re-arm + при необходимости другие эффекты)

### Простой вариант буферизации событий

Чтобы не парсить типы событий в blocking cmd, watcher-ы сессий отправляют свои события в `statusWatcher.Events` через общий канал — нет, проще:

- На каждом `<id>/` создаём свой `fsnotify.Watcher` ИЛИ используем родительский watcher и фильтруем по пути в `Update`.
- **Проще:** один watcher на `sessionBaseDir()` ловит ВСЕ события в нём и ниже (recursive — нет, fsnotify не recursive; придётся подписываться на каждый подкаталог). 
- **Реализация:** на `sessionBaseDir()` watcher ловит CREATE/REMOVE/RENAME подкаталогов (для управления списком `sessionWatchers`), а на каждом `<id>/` — отдельный watcher для `status.json`.

### Проблема multi-watcher overhead

Если у пользователя 100+ сессий, создавать 100+ watcher-ов дорого. **Решение:** ограничить watcher только на подкаталоги, которые соответствуют items в `tree.AllItems()` (то есть только на реальные чаты пользователя, не на архив). Это согласуется с тем, что бейджи нужны только для видимых чатов.

### Подсчёт видимых items

```go
keys := make(map[string]bool)
for _, it := range a.tree.AllItems() {
    if it == nil || it.IsFolder || it.IsTerminal {
        continue
    }
    if key := a.tree.SessionKeyOf(it); key != "" {
        keys[key] = true
    }
}
```

Поддерживаем `sessionWatchers` в синхронизации с `keys`: добавляем для новых, удаляем для исчезнувших.

### Закрытие

В `main.go` сейчас нет явного shutdown — `tea.Quit` и так завершает процесс. Watcher-ы закроются через OS. Но добавим `closeStatusWatcher()` (вызывать из `Update` если какого-то флага — лишнее, OS cleanup хватит).

### Тесты

1. `TestStatusWatcherSetupCreatesWatchers` — `newApp("test", "")` + создать temp dir с `sessionBaseDir/sess1/status.json` + вызвать `setupStatusWatcher()`; проверить что `statusWatcher != nil` и `sessionWatchers["sess1"] != nil`.
2. `TestRefreshTreeBadgesOnStatusEvent` — записать `status.json` с action="thinking", вызвать `refreshTreeStatusBadges`, проверить что `tree.StatusBadge(item) != ""` (или эмодзи есть в map).
3. `TestWatchTreeStatusCmdReturnsNilWithoutWatcher` — если watcher не создан, cmd == nil.
4. `TestTreeStatusChangedMsgTriggersRefresh` — послать msg в Update, проверить что вызвался refresh (через `tree.SetStatusBadges` spy).

Тесты для watcher-ов с реальным fsnotify флапают на CI — поэтому:
- `setupStatusWatcher` принимает `fsnotify.NewWatcher` через DI (через поле `newWatcherFn`), в тестах подменяем на fake, который кладёт события вручную в канал.
- Либо делаем через injectable watcher interface (тип `eventSource` с `Events() <-chan fsnotify.Event`).

## Критерии приёмки

- [x] `App` создаёт fsnotify-watcher на `sessionBaseDir()` и на каждый видимый подкаталог сессии при `Init`.
- [x] Изменение `status.json` для любой сессии из видимых мгновенно обновляет бейдж в Tree.
- [x] Удаление watcher-а при удалении сессии (исчезновение из `tree.AllItems()`) — нет утечки дескрипторов.
- [x] Если `sessionBaseDir()` не существует — `Init` создаёт его, watcher не паникует, бейджи остаются пустыми до появления сессий.
- [x] `refreshTreeStatusBadges` больше не dead code — вызывается из `Init` и из обработчика `treeStatusChangedMsg`.
- [x] `gofmt`, `go vet ./...`, `go test ./... -count=1` зелёные (включая e2e).
- [x] Бинарник `automata` пересобран `go build -o automata .`.
- [x] Live-сессии не перезапускаются автоматически (новый код подхватится при следующем старте).
