# Восстановление сессии терминала

## Контекст
Терминальные элементы в Automata запускают shell в PTY, но при закрытии/перезапуске терминала теряются:
- текущая рабочая директория (cwd);
- история введённых команд.

Пользователь ожидает, что при повторном открытии терминала shell стартует в последнем cwd, а введённые команды сохраняются.

## Цель
Добавить персистентность cwd и истории команд для терминальных элементов: отслеживать cwd через OSC 7, сохранять в `state.json` и восстанавливать при открытии.

## Что изменилось

1. `internal/tree/model.go` — поля `CWD` и `CommandHistory` в `Item` и `StateItem`.
2. `internal/tree/state.go` — сериализация/десериализация новых полей.
3. `internal/term/ansi.go` — распознавание OSC 7 последовательностей и извлечение пути.
4. `internal/term/emulator.go` — callback `OnCWDChange`, запуск shell в сохранённом cwd, сбор истории команд.
5. `internal/term/pty.go` — `SpawnInDir`, закрытие `Errors` канала при EOF.
6. `main.go` — передача cwd в `SessionManager.openItem`, обработка `PtyExitMsg`.

## Детали реализации

### 1. Данные в дереве

```go
// Item (model.go)
type Item struct {
    // ... existing fields ...
    CWD            string
    CommandHistory []string
}

// StateItem (state.go)
type StateItem struct {
    // ... existing fields ...
    CWD            string   `json:"cwd,omitempty"`
    CommandHistory []string `json:"command_history,omitempty"`
}
```

- `CWD` — абсолютный путь, например `/Users/a/Space/Projects/HumanHorizon/automata`.
- `CommandHistory` — последние 1000 команд (FIFO, старые вытесняются).

### 2. Отслеживание cwd через OSC 7

Shell настраивается через env-переменную `PROMPT_COMMAND` перед запуском. Bash читает её из окружения при старте.

```bash
PROMPT_COMMAND='echo -ne "\033]7;${PWD}\007"'
```

Выбран `echo -ne` вместо `printf`, потому что `printf` без завершающего `\n` оставляет курсор на той же строке и нарушает перерисовку prompt.

Emulator перед запуском PTY добавляет `PROMPT_COMMAND` в env для `bash`.

### 3. Парсинг OSC 7 в Parser

Реализован в `internal/term/ansi.go`:
- состояние `stateOSC` накапливает payload до `BEL` (`\x07`) или `ESC \` (`\x1b\\`).
- `handleOSC` вызывает `onCWD` для OSC 7 (`7;` или `7;file://...`).
- `extractOSC7Path` убирает префикс `file://host`, оставляя абсолютный путь.

### 4. Callback flow и deadlock fix

- `Parser.SetCWDCallback` устанавливает callback.
- `Emulator.Start()` устанавливает callback, который под мьютексом обновляет `e.cwd` и вызывает `OnCWDChange`.
- **Критично:** `Emulator.Update(PtyOutputMsg)` отпускает `e.mu` перед вызовом `parser.Feed`, иначе callback попытается взять уже захваченный мьютекс → deadlock.
- Callback обновляет `item.CWD` и дергает `SessionManager.debouncedSave()` (не чаще раза в секунду).

### 5. История команд

- При нажатии `Enter` в `Emulator.handleKey` считывается текущая строка экрана (`Screen.LineText`).
- `stripPrompt` удаляет prompt (`$ `, `# `, `> `, `% `).
- Команда дедуплицируется и добавляется в `commandHistory` с лимитом 1000.
- `OnCommandHistoryChanged` → `debouncedSave()`.

### 6. Запуск в сохранённом cwd

- `Pty.SpawnInDir` принимает рабочую директорию и запускает `exec.Command` с `cmd.Dir = dir`.
- `Emulator.spawnPty` выбирает `SpawnInDir`, если `e.initialCWD != ""`.
- `SessionManager.openItem` передаёт `item.CWD` через `em.SetInitialCWD`.

### 7. Завершение сессии и повторное открытие

- `Pty.readLoop` закрывает канал `Errors` при EOF/ошибке/закрытии.
- `Emulator.Listen` получает `PtyExitMsg` и передаёт его вверх.
- `App.Update` обрабатывает `term.PtyExitMsg` через `SessionManager.StopSessionBySessionID`.
- `StopSessionBySessionID` вызывает `em.Stop()` (ASCII-арт) и удаляет сессию из `sm.sessions`.
- При повторном клике `openItem` создаёт новый `Emulator` с `initialCWD = item.CWD`.

### 8. ASCII-арт после остановки

- `Emulator.stopped` включается при `Stop()`.
- `Emulator.View` рисует центрированный ASCII-арт AI-чата вместо замороженного терминала.

## Критерии приёмки

- [x] Терминал запоминает cwd после `cd` внутри сессии.
- [x] После закрытия и повторного открытия терминала shell стартует в сохранённом cwd.
- [x] После перезапуска Automata cwd терминала восстанавливается.
- [x] История команд сохраняется между открытиями терминала.
- [x] История команд переживает перезапуск Automata.
- [x] OSC 7 не отображается на экране терминала.
- [x] `go test ./...`, `go vet ./...`, `go build` проходят.
