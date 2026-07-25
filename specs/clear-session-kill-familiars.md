# Clear должен убивать фамильяров этой сессии

## Контекст

Кнопка `× Clear` в таб-баре чата сейчас очищает только главную сессию: останавливает её `*portalis.Emulator`, удаляет JSONL и перезапускает PTY с тем же `sessionID`. Если эта сессия успела породить фамильяров (через `summon_familiar`), они остаются жить — у каждого свой `*portalis.Emulator` со своим JSONL, и они по-прежнему отображаются как табы. Это противоречит семантике Clear: «начать сессию с чистого листа».

Связь фамильяра с владельцем выражается через `PI_OWNER_SESSION=<mainSessionID>` в env при старте эмулятора (`internal/ui/chat_panel.go::addFamiliar`) и через `familiars.json` в каталоге сессии. Каталог сессии: `~/.ai/automata[profiles/<profile>]/sessions/<sessionID>/familiars.json`.

Отдельная, несвязанная бага — `clear-session-replace-panel-em.md` (после Clear пустой экран у главной сессии). В рамках этого тикета не правится.

## Цель

При нажатии `× Clear` на главной сессии:

1. Остановить `*portalis.Emulator` каждого фамильяра этой сессии.
2. Удалить JSONL историю каждого фамильяра.
3. Убрать записи фамильяров из `familiars.json` (тогда `checkFamiliars` сам уберёт табы на ближайшем 3-секундном цикле).
4. Очистить `emulatorCache`, `activeSessions` и любые внутренние карты `ChatPanel`, относящиеся к этим фамильярам.
5. Перезапустить главный эмулятор (существующее поведение сохраняется).

Порядок важен: сначала стоп эмуляторов → удаление JSONL → очистка `familiars.json` → перезапуск главной сессии. Если убрать запись из `familiars.json` до остановки эмулятора, фамильяр может успеть переписать файл и запись вернётся.

## Что изменится

### `internal/ui/chat_panel.go`
- Добавить экспортёры (нужны и для будущего `clear-session-replace-panel-em.md`):
  - `func (cp *ChatPanel) Sessions() []*chatSession` — возвращает срез всех табов.
  - `func (s *chatSession) Em() *portalis.Emulator` — возвращает эмулятор сессии.
  - `func (s *chatSession) FamiliarID() string` — возвращает `familiarID` (пусто для Main).
- Добавить метод `func (cp *ChatPanel) FamiliarSessionIDs() []string` — собирает `familiarID` со всех табов, кроме Main (где `familiarID == ""`).

### `internal/ui/chat_panel_test.go`
- Добавить `TestFamiliarSessionIDsReturnsNonMainOnly` — на фикстуре из 1 Main + 2 фамильяров проверяет, что метод возвращает ровно два `familiarID` в любом порядке.
- Добавить `TestRemoveFamiliarCleansKnown` — `removeFamiliar` уже удаляет из `cp.sessions`; добавить проверку, что соответствующий `id` вычищается из `cp.known`.

### `main.go::clearSessionCmd`
- Сигнатура расширяется: `func (a *App) clearSessionCmd(sessionID, cwd string, familiarSIDs []string) tea.Cmd`.
- Внутри `tea.Cmd`-функции, **до** перезапуска главного эмулятора, для каждого `sid` из `familiarSIDs`:
  1. `em.Stop()` и `delete(a.emulatorCache, sid)` (если есть в кэше).
  2. `delete(a.activeSessions, sid)`.
  3. `paths.DeleteSessionJSONL(sid, cwd)` — лучшее усилие, ошибка только логируется (consistent с существующим поведением для главной сессии).
- После удаления всех JSONL — очистить `familiars.json`: записать `[]` в файл, адрес вычисляется тем же путём, что в `ChatPanel.familiarStatePath()` (нужно вынести в `internal/paths` или пробросить путь). Лучший вариант: вынести `familiars.json` path helper в `internal/paths/sessionfile.go` и переиспользовать из `ChatPanel`.
- Существующая логика перезапуска главного эмулятора и возврата `PtyReadyMsg` сохраняется без изменений.

### `internal/paths/sessionfile.go`
- Добавить `func FamiliarsJSONLPath(profile, sessionID string) (string, error)` — резолвит путь к `familiars.json` для данной сессии и профиля. Уважает `~/.ai/automata/profiles/<profile>/sessions/<sessionID>/familiars.json`.
- Добавить `func ClearFamiliarsJSONL(profile, sessionID string) error` — пишет `[]` в этот файл (или удаляет, если пустой массив == отсутствие файла для `loadFamiliars`). Выбираем **запись `[]`**: при отсутствии файла `loadFamiliars` возвращает `nil`, что семантически отличается от «явно пусто», и `checkFamiliars` тогда не уберёт уже-созданные табы (в текущем коде это допустимо, но при рефакторинге может сломаться).

### `main.go` (в месте создания `cp`)
- Callback `SetOnClearSession` собирает `cp.FamiliarSessionIDs()` и передаёт в `clearSessionCmd`:
  ```go
  cp.SetOnClearSession(func(sid, cwd string) tea.Cmd {
      return a.clearSessionCmd(sid, cwd, cp.FamiliarSessionIDs())
  })
  ```

### `main_view_test.go`
- Расширить `TestClearRestartsChatWithConfiguredPiAgentDir` или добавить новый `TestClearKillsFamiliarsOfThisSession`:
  - Создать `App` с минимальной структурой.
  - Создать `ChatPanel` через `container.SetChat` с 1 Main + 2 фамильярами. Эмуляторы фамильяров — моки через `startEmulatorSyncFn`-инъекцию (как уже делается для главной сессии) или через заглушку в `emulatorCache`.
  - Подложить фейковые JSONL-файлы фамильяров в `t.TempDir()` и убедиться, что `DeleteSessionJSONL` их находит (через тот же fallback-механизм).
  - Подложить `familiars.json` с двумя записями.
  - Вызвать `clearSessionCmd(mainSID, cwd, cp.FamiliarSessionIDs())`.
  - Проверить:
    1. `emulatorCache` не содержит sessionID фамильяров.
    2. JSONL-файлы фамильяров удалены.
    3. `familiars.json` существует и содержит `[]`.
    4. `emulatorCache[mainSID]` указывает на новый эмулятор.
    5. `activeSessions` содержит только `mainSID`.

## Детали реализации

1. **Порядок операций внутри `clearSessionCmd`:**
   ```text
   1. for sid in familiarSIDs:
        - em = emulatorCache[sid]; if exists { em.Stop(); delete(emulatorCache, sid) }
        - delete(activeSessions, sid)
   2. for sid in familiarSIDs:
        - paths.DeleteSessionJSONL(sid, cwd)  // best-effort, log on error
   3. paths.ClearFamiliarsJSONL(profile, mainSessionID)
   4. // existing logic: stop main emulator, delete main JSONL, recreate, StartSync
   ```

2. **`DeleteSessionJSONL` для фамильяров:** функция уже умеет fallback-поиск по всем каталогам хранилища, поэтому для фамильяров, чей JSONL лежит в каталоге другой сессии (что маловероятно, но возможно при миграциях), сработает тот же fallback.

3. **`familiars.json` очистка vs удаление:** выбираем запись `[]` ради атомарности и совместимости с `checkFamiliars` (которая читает файл, и при отсутствии файла возвращает `nil`, что означает «нет фамильяров» — но семантика «удалено пользователем» лучше выражается явным `[]`).

4. **Профиль:** `familiars.json` лежит под профилем. У `App` есть `a.profile`. Передаём его в `ClearFamiliarsJSONL`.

5. **`familiarSIDs` пустой:** если фамильяров нет — фаза 1-3 просто пропускается (no-op), фаза 4 работает как раньше. Это сохраняет существующий регрессионный тест `TestClearRestartsChatWithConfiguredPiAgentDir` без изменений.

## Критерии приёмки

- [ ] `ChatPanel.FamiliarSessionIDs()` возвращает ровно ID не-Main табов в любом порядке.
- [ ] После `Clear` `emulatorCache` не содержит sessionID фамильяров; JSONL фамильяров удалены; `familiars.json` существует и парсится как `[]`.
- [ ] Главная сессия перезапускается с тем же `sessionID` (существующее поведение сохраняется).
- [ ] Регрессионный тест `TestClearKillsFamiliarsOfThisSession` падает до фикса и проходит после.
- [ ] Существующий `TestClearRestartsChatWithConfiguredPiAgentDir` продолжает проходить без изменений.
- [ ] `go vet ./...` проходит.
- [ ] `go test ./internal/...` и `go test .` проходят.
- [ ] `go build -o automata .` проходит; mtime `./automata` свежий.
- [ ] Ручной сценарий: открыть чат → создать фамильяра (`summon_familiar`) → подождать пока появится таб → Clear → таб фамильяра исчезает, JSONL фамильяра удалён, главная сессия показывает свежий `pi` prompt.

## Что НЕ делается в этом тикете

- Баг «после Clear пустой экран у главного таба» (`clear-session-replace-panel-em.md`) — отдельная задача.
- Удаление файлов планов/задач фамильяров — отдельная задача (если потребуется).
- Изменение поведения Clear для не-главных табов (например, Clear на табе фамильяра) — не требуется по текущему тикету.