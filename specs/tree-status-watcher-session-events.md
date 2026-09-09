# Per-session status watcher: запустить cmd-chain для каждого sessionWatcher

## Контекст

02.08 был реализован `tree-status-watcher.md` — event-driven Tree-бейджи через `fsnotify`. Создавались два уровня watcher-ов:

- `App.statusWatcher` — на `sessionBaseDir()` (родительский каталог всех сессий).
- `App.sessionWatchers` — `map[sessionID]*fsnotify.Watcher`, по watcher-у на каждый видимый чат (`<sessionID>/`).

Для `statusWatcher` была сделана blocking cmd-chain: `watchTreeStatusCmd()` блокирует на `<-w.Events` и возвращает `treeStatusChangedMsg`; `Update` ловит msg и re-arm.

Для **per-session watcher-ов** цепочка не была реализована: `syncSessionWatchers` создаёт `fsnotify.NewWatcher()` + `sw.Add(dir)` и сохраняет в `a.sessionWatchers[key]`, но **никто не читает `sw.Events`**. Горутины-читателя нет.

Результат: parent watcher ловит только события **в самой директории** `sessions/` (CREATE/REMOVE подкаталогов), а запись в `status.json` (файл внутри `<sessionID>/status.json`) проходит мимо. Tree-бейджи не обновляются.

Доказательство (логи диагностики 12.08 17:23):

```
DIAG: setupStatusWatcher parent watcher up, base=.../sessions
DIAG: syncSessionWatchers visible=23 new_attached=21 attach_errors=0 total_watchers=21
```

После записи в `status.json` (через `WriteFile`) ни одного `parent watcher event received` или per-session события не появляется — потому что parent watcher на это не подписан, а per-session watcher никто не слушает.

## Цель

Запустить blocking cmd-chain для каждого per-session watcher-а, чтобы запись в `<sessionID>/status.json` сразу триггерила `treeStatusChangedMsg` → `recomputeTreeStatusBadges()` → обновление Tree.

## Что изменится

1. `internal/main.go` — добавить `func (a *App) watchSessionCmd(key string) tea.Cmd`:
   - возвращает `nil`, если `a.sessionWatchers[key]` отсутствует;
   - иначе — блокирующее чтение `_, ok := <-sw.Events`, при `ok == false` возвращает `nil` (watcher закрыт), при успехе — `treeStatusChangedMsg{}`.

1a. `internal/main.go` — добавить `func (a *App) rearmSessionWatchers() []tea.Cmd`:
   - для каждого ключа в `a.sessionWatchers` добавить `a.watchSessionCmd(key)` в возвращаемый слайс;
   - используется в `case treeStatusChangedMsg` чтобы cmd-chain per-session watcher-ов перезапускалась после каждого события (одно блокирующее чтение — это one-shot).

2. `internal/main.go::syncSessionWatchers` — сменить сигнатуру: `func (a *App) syncSessionWatchers() []tea.Cmd`. Внутри:
   - для каждого **нового** прикреплённого watcher-а добавить `a.watchSessionCmd(key)` в возвращаемый слайс;
   - для удалённых watcher-ов — ничего (их cmd-chain при следующем event вернёт `nil` через закрытый `Events` и естественно умрёт);
   - **existing** watcher-ы (уже в `a.sessionWatchers`) — НЕ добавлять, их cmd-chain перезапускается отдельно через `rearmSessionWatchers`.

3. `internal/main.go::Init` — после `a.setupStatusWatcher()` + `a.recomputeTreeStatusBadges()`:
   - собрать `newCmds := a.syncSessionWatchers()` (здесь все 21 новых, потому что `a.sessionWatchers` пустой);
   - `tea.Batch` дополнить этими cmd-ами.

4. `internal/main.go::Update case treeStatusChangedMsg` — после `a.syncSessionWatchers()` и `a.recomputeTreeStatusBadges()`:
   - собрать `allCmds := newCmds` (из syncSessionWatchers) + `a.rearmSessionWatchers()` (re-arm ВСЕХ существующих per-session cmd-chain);
   - добавить `a.watchTreeStatusCmd()` для parent (как сейчас);
   - вернуть `tea.Batch(allCmds...)` либо `nil`, если пусто.

5. Убрать все 4 диагностических `log.Printf("DIAG: ...")` строки, добавленные для поиска этого бага: в `setupStatusWatcher`, `syncSessionWatchers`, `recomputeTreeStatusBadges`, `watchTreeStatusCmd`.

## Детали реализации

1. `watchSessionCmd(key)`:
   ```go
   func (a *App) watchSessionCmd(key string) tea.Cmd {
       sw, ok := a.sessionWatchers[key]
       if !ok {
           return nil
       }
       return func() tea.Msg {
           _, ok := <-sw.Events
           if !ok {
               return nil
           }
           return treeStatusChangedMsg{}
       }
   }
   ```

2. `syncSessionWatchers` должен накапливать только `newAttached++`-ветку. Логика:
   ```go
   var cmds []tea.Cmd
   ...
   if _, ok := a.sessionWatchers[key]; ok {
       continue
   }
   ...
   a.sessionWatchers[key] = sw
   newAttached++
   cmds = append(cmds, a.watchSessionCmd(key))
   ...
   return cmds
   ```

3. В `case treeStatusChangedMsg`:
   ```go
   case treeStatusChangedMsg:
       a.statusWatchPending = false
       newCmds := a.syncSessionWatchers()
       a.recomputeTreeStatusBadges()
       var allCmds []tea.Cmd
       allCmds = append(allCmds, newCmds...)
       allCmds = append(allCmds, a.rearmSessionWatchers()...)
       if cmd := a.watchTreeStatusCmd(); cmd != nil {
           a.statusWatchPending = true
           allCmds = append(allCmds, cmd)
       }
       if len(allCmds) > 0 {
           return a, tea.Batch(allCmds...)
       }
       return a, nil
   ```

4. **Идемпотентность**: один per-session watcher = одна cmd-chain. Никогда не запускать вторую cmd-chain для уже отслеживаемого watcher-а. `syncSessionWatchers` для existing watcher-ов возвращает nil (continue), а cmd добавляется только в ветке «только что созданный watcher». `rearmSessionWatchers` запускает новые cmd для уже существующих watcher-ов после каждого `treeStatusChangedMsg` — это перезапуск, а не дублирование, потому что предыдущая cmd-chain уже завершилась (вернула msg и завершилась — blocking read one-shot).

5. **Удаление watcher-а**: при удалении чата из Tree (close+delete в syncSessionWatchers) старая cmd-chain продолжит блокировать на `<-sw.Events`, но `sw` закрыт → канал закрыт → `<-sw.Events` сразу вернёт `(_, false)` → cmd вернёт `nil`. Горутина завершится естественно. Cancelling не требуется.

## Критерии приёмки

- [ ] `watchSessionCmd(key)` существует и компилируется.
- [ ] `syncSessionWatchers` возвращает `[]tea.Cmd` с cmd-ами только для новых watcher-ов.
- [ ] `Init` запускает cmd-chain для всех начальных per-session watcher-ов (после `setupStatusWatcher`).
- [ ] `case treeStatusChangedMsg` запускает cmd-chain для **новых** watcher-ов, прикреплённых при sync.
- [ ] Все 4 `DIAG:` лог-строки удалены.
- [ ] Регрессионный тест `TestPerSessionWatcherRearmsAfterEvent` (в `main_view_test.go`):
  - создаёт `App` с `setupStatusWatcher()` + `syncSessionWatchers()`;
  - дожидается первого `treeStatusChangedMsg` от `watchSessionCmd(key)()` при записи в `status.json`;
  - вызывает `rearmSessionWatchers()` — возвращает непустой слайс с cmd для нашего key;
  - запускает свежий `watchSessionCmd(key)()` (как будто re-arm cmd попал в bubbletea loop);
  - пишет второй раз в `status.json`;
  - получает второй `treeStatusChangedMsg` в течение 3 секунд. Без re-arm этот тест зависает на таймауте.
- [ ] Существующие `TestSetupStatusWatcher*`, `TestSyncSessionWatchers*`, `TestWatchTreeStatusCmd*`, `TestRecomputeTreeStatusBadges*` продолжают проходить без изменений в продакшен-логике.
- [ ] `go vet ./...` проходит.
- [ ] `go test ./internal/...` проходит.
- [ ] `go build -o automata .` проходит.
- [ ] Полный ручной сценарий: запустить автомату → записать в `status.json` для любого видимого чата → Tree-бейдж обновляется в течение 1 секунды.
