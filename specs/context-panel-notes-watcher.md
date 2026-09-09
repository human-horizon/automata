# Реактивные notes в ContextPanel

## Контекст

В FolderMode (когда выбрана папка) правая панель — это `ContextPanel`. Она показывает:
- **Content** (notes из `~/.ai/automata/profiles/<profile>/domains/<domain>/notes.json`)
- **Kanban** (задачи домена)

В ChatMode правая панель — это `KnowledgePanel`, которая показывает status/plans/jobs сессии, но НЕ notes. Notes привязаны к домену (= папке), не к сессии — это by design.

После `watch-jobs-panel` (2026-07-31) KnowledgePanel стал реактивным через fsnotify на status.json/plans.json/settings.json/jobs/. Но ContextPanel остался без watcher-а — он обновляется только при `SetDomain` (т.е. когда пользователь выбирает другую папку и возвращается). Аня подтвердила: "надо перезапускать чтобы увидеть" обновлённые notes.

## Цель

`ContextPanel` реактивно обновляет notes при изменении `notes.json` в текущем домене. Без periodic-тика.

## Что изменится

1. `internal/ui/context_panel.go` — добавить `notesWatcher *fsnotify.Watcher` + `notesWatchPending bool`; `setupNotesWatcher()` создаёт watcher на `domainDir(profile, domain)`; `watchNotesCmd()` blocking cmd; `notesChangedMsg` в `Update` с refresh + re-arm; `closeNotesWatcher()` для очистки при `SetDomain` / shutdown.
2. `internal/ui/context_panel_test.go` (если нет — создать) — тест: создать domain, записать notes.json, проверить что `c.data` обновился без ручного `Refresh()`.
3. `CONTEXT.md` — запись о фиксе.

## Детали реализации

### Поля

```go
type ContextPanel struct {
    // ... существующие
    notesWatcher     *fsnotify.Watcher
    notesWatchPending bool
}
```

### Жизненный цикл

- `NewContextPanel` — без watcher (domain ещё не задан).
- `SetDomain(domain)`:
  - Если новый domain == старый → return (уже было).
  - Закрыть старый watcher (если был).
  - Поставить новый domain.
  - Вызвать `setupNotesWatcher()`.
  - `refresh()` уже есть.
- `setupNotesWatcher()`:
  - Если `c.domain == ""` → return.
  - Вычислить `domainDir(c.profile, c.domain)`.
  - Если каталог не существует → `os.MkdirAll(...)`, потом watcher.
  - `fsnotify.NewWatcher()` + `w.Add(domainDir)`.
  - Сохранить в `c.notesWatcher`.
- `watchNotesCmd()`:
  - Если `c.notesWatcher == nil` → return nil.
  - `func() tea.Msg { _, ok := <-w.Events; if !ok { return nil }; return notesChangedMsg{} }`.
- `Update` обработка `notesChangedMsg`:
  - `c.notesWatchPending = false`.
  - `c.refresh()` (перечитывает notes).
  - re-arm: вернуть `c.watchNotesCmd()` и поставить `c.notesWatchPending = true`.
- Закрытие:
  - В `SetDomain` — закрыть старый watcher.
  - Watcher-ы закрываются через OS при exit, но добавим `closeNotesWatcher()` для аккуратности (вызывается из SetDomain).

### Edge case: domain не существует

`notes.json` создаётся при первом сохранении notes. Если watcher создан до того как каталог существует — `w.Add()` вернёт ошибку. Решение: `MkdirAll` перед watcher (если знаем profile+domain), watcher пересоздаётся при следующем событии на parent.

`domainDir` использует `os.Getenv("AI_PROFILE")` если `profile == ""`. Это означает, что в тестах с изолированным `AI_DATA_HOME` watcher укажет на temp dir, а notes.json создастся там же. Корректно.

### Профиль = ""

`domainDir` берёт `AI_PROFILE` из env. Если не задан, `profileSlug()` = "default". Watcher создаётся в `~/.ai/automata/profiles/default/domains/<domain>/`. Подходит для default-профиля.

## Критерии приёмки

- [x] `ContextPanel.SetDomain` создаёт watcher на `domainDir(profile, domain)`.
- [x] Изменение `notes.json` в текущем домене мгновенно обновляет `c.data` без ручного `Refresh()`.
- [x] `SetDomain` с другим domain закрывает старый watcher (нет утечки fd).
- [x] `notesChangedMsg` re-arm через `notesWatchPending` (не стекает).
- [x] `gofmt` / `go vet ./...` / `go test ./...` зелёные.
- [x] Бинарник `./automata` пересобран.
- [x] Существующая функциональность Content/Kanban tabs НЕ сломана.
