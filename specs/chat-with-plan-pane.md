# Chat with plan pane

## Контекст
При открытии чата в Automata пользователь видит только терминал с `just-pi`.
Для удобства рядом с чатом нужно показывать план, задачи и статус сессии из `ai-knowledge`.

## Цель
Добавить справа от терминала чата панель `ai-knowledge --plan`, которая запускается автоматически и не мешает работе чата.

## Архитектура

```
┌───────────────┬───────┐
│  just-pi      │ plan  │
│  terminal     │ panel │
│               │       │
└───────────────┴───────┘
```

- `internal/ui/container.go` — режим `ChatWithPlanMode`.
- `internal/ui/context_panel.go` — обёртка `ContextPanel`, запускает `ai-knowledge` через `term.Emulator`.
- `main.go` — для чатов вызывает `container.SetChat(sel, sessionID)`.

## Детали реализации

### 1. Container modes

```go
const (
    ChatMode ContainerMode = iota
    ChatWithPlanMode
    FolderMode
)
```

### 2. ContextPanel

- `SetPlanSession(sessionID)` — переключает панель в режим `ai-knowledge -s <sessionID> --plan`.
- `SetDomain(domain)` — переключает панель в режим `ai-knowledge -d <domain>` (для folder preview).
- `Start()` — создаёт `term.Emulator` и спавнит процесс.
- `Update` — если `emulator == nil`, стартует на `WindowSizeMsg` или `warp.ResizeMsg`; перед стартом переводит сообщение в `portalis.ResizeMsg`, чтобы эмулятор получил корректный размер панели.

### 3. Запуск без зависания

- `ContextPanel.Start()` возвращает `tea.Cmd` (асинхронно).
- `Container.SetChat` только настраивает режим и session ID, не вызывает `Start` синхронно.
- `App.Update(ItemSelectedMsg)` возвращает `tea.Batch(BroadcastResize, em.Start())`.
- `BroadcastResize` теперь `tea.Cmd` и рассылает `warp.ResizeMsg`, на который `ContextPanel` стартует.

Таким образом plan-панель стартует асинхронно после layout, не блокируя текущий `Update`.

### 4. Mouse resize

- `Container.handleChatWithPlanMouse` позволяет тянуть границу между терминалом и plan-панелью.
- `planWidth` (int, дефолт 40) — фиксированная ширина plan-панели в символах.
- При drag обновляется `planWidth` и вызывается `onPlanWidthChange` для сохранения в state.
- `planWidth` сохраняется в `state.json` как `plan_width` и восстанавливается при старте.

### 5. Collapse

- Plan-панель сворачивается в 1 символ (`>`) при клике по шеврону.
- Клик по любому месту свёрнутой панели разворачивает её.
- При коллапсе warp-бордер не рисуется (изменение в warp).

## Критерии приёмки

- [x] Открытие чата показывает терминал слева и plan-панель справа.
- [x] Plan-панель запускается автоматически.
- [x] Открытие чата не приводит к зависанию.
- [x] Мышью можно менять ширину панелей.
- [x] Терминал и plan-панель не ломаются при resize окна.
