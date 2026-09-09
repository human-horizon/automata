# Familiar-табы: серый цвет + крестик для удаления с подтверждением

## Контекст

Сейчас `ChatPanel.renderTabBar` (internal/ui/chat_panel.go:383) рисует main-таб и familiar-табы **одинаково**: `Color("4")` (синий) для активного, `Color("8")` (bright black) для неактивного. Визуально нельзя отличить «это главный чат» от «это фоновый фамильяр». Также нельзя закрыть фамильяр — единственная кнопка `× Clear` справа убивает главную сессию.

Аня: «Измени цвет табов фамильяров» (выбран **серый**) «и дай возможность их удалять по крестику справа в табе» (полное закрытие **с подтверждением**).

## Цель

1. Familiar-табы визуально отличаются от main-таба — серый фон (а не синий для активных и тот же серый для неактивных).
2. Каждый familiar-таб имеет `×` справа; клик → модальное подтверждение → подтверждение → удаление фамильяра (эмулятор + JSONL + familiars.json).

## Что изменится

`internal/ui/chat_panel.go`:

### 1. Стили

Стили familiar-tab строятся из текущей семантической палитры Automata, чтобы
переключение темы применялось без отдельного hardcode: неактивный tab использует
`palette.Surface`/`palette.TextMuted`, активный — `palette.Raised`/`palette.TextStrong`,
а close-control — `palette.Error`/`palette.TextStrong`. Это соответствует
`specs/themes-and-settings.md`: обычные UI-тексты и фоны остаются нейтральными
серо-тёплыми.

### 2. `renderTabBar` — различать main и familiar

- main-таб (`s.familiarID == ""`) — текущие стили (blue/grey)
- familiar-таб (`s.familiarID != ""`) — `familiarTabActiveStyle` если активный, `familiarTabInactiveStyle` если нет
- Для familiar-табов справа добавляется пробел + `×` (рендерится через `familiarCloseBtnStyle`). `×` рендерится как **часть таба**, чтобы клик мышью попадал в `tabLen`-расчёт.

```go
tabLen := len(s.name) + 2 // " name "
if s.familiarID != "" {
    tabLen += 2 // " ×" (1 padding + 1 close button)
}
```

### 3. Подтверждение — модальное состояние

Добавить в `ChatPanel`:
```go
type ChatPanel struct {
    // ...existing fields...
    pendingCloseFamiliar string // familiarID awaiting y/n confirmation
    closeFamiliarModal   *warp.Modal
}
```

`pendingCloseFamiliar != ""` означает: поверх чата рисуется `warp.Modal` с
текстом «Close familiar `<name>`?» и кнопками Yes/No.

### 4. `handleMouse` — клик на крестик фамильяра

В цикле по табам, после определения попадания в таб:
```go
if i != cp.activeIdx || s.familiarID != "" {
    // ... existing tab-switch logic for main tabs ...
}
```

Дополнительно: если клик в зоне `×` (последние две колонки таба, где
`s.familiarID != ""`), установить `cp.pendingCloseFamiliar = s.familiarID` и
создать `cp.closeFamiliarModal`.

### 5. `Update` — обработка подтверждения

Когда `cp.pendingCloseFamiliar != ""`:
- `KeyMsg.String() == "y"` или `"Y"` → вызвать `cp.onCloseFamiliar(pendingCloseFamiliar)`, очистить pending и overlay.
- `KeyMsg.String() == "n"` или `"N"` или `"esc"` → очистить pending и overlay, вернуть nil.

### 6. Новый callback в `ChatPanel`

```go
type ChatPanel struct {
    // ...existing...
    onCloseFamiliar func(familiarID string, em *portalis.Emulator)
}
```

`internal/main.go` реализует `onCloseFamiliar`:
- вызывает `em.Stop()` (emulator для знакомого)
- удаляет JSONL через `paths.DeleteSessionJSONL(familiarSessionID, em.CWD(), a.piAgentDir)`
- удаляет знакомого из `a.emulatorCache` и `a.activeSessions`
- чистит `familiars.json` через `paths.ClearFamiliarsJSONL` (или удаляет entry — нужна новая функция `RemoveFamiliar`)
- ChatPanel синхронно убирает tab, а callback очищает host-side state; polling
  получает актуальный `familiars.json` без повторного появления tab

### 7. `internal/paths/sessionfile.go` — `RemoveFamiliar`

```go
// RemoveFamiliar deletes the entry for familiarID from
// <profile>/sessions/<sessionID>/familiars.json. The file is rewritten as
// a pretty-printed JSON array. A missing file or missing entry is a no-op.
func RemoveFamiliar(profile, sessionID, familiarID string) error
```

### 8. Общий helper удаления tab

`ChatPanel.removeSessionAt(index)` останавливает panel, удаляет tab и корректирует
`activeIdx`: при удалении tab перед активным индекс уменьшается, при удалении
активного выбирается соседний tab.

## Детали реализации

1. **Расположение крестика**: после `name` в табе, через 1 пробел. Полная ширина familiar-таба = `1 + len(name) + 1 + 1 + 1` = `len(name) + 4`. Логика клика: `clickX >= tabStart + 1 + len(name) + 1 && clickX < tabStart + len(name) + 4` — попадание в зону `×`.

2. **Активный familiar и крестик**: для активного фамильяра `×` тоже кликабелен — закрытие не требует «сначала деактивировать».

3. **Подтверждение как overlay**: `cp.View(width, height)` рисует поверх всего чата (или центрирует) `cp.confirmOverlay` когда `pendingCloseFamiliar != ""`. Overlay блокирует ввод под собой.

4. **Стилистика overlay**:
   ```
   ┌────────────────────────────┐
   │ Close familiar "tester-1"? │
   │                            │
   │  [Y]es  /  [N]o            │
   └────────────────────────────┘
   ```

5. **`familiars.json` формат**: текущая схема (из `paths.FamiliarsJSONLPath`) — массив `{id, sessionId, created}`. После `RemoveFamiliar` файл перезаписывается без удалённого entry.

6. **`emulatorCache` и `activeSessions`**: после `em.Stop()` удалить обе записи для `familiarID`.

7. **`familiarRemovedMsg`**: типизированное сообщение, обрабатывается в `ChatPanel.Update` для удаления tab. После удаления сохраняется активный tab, если удалён предыдущий неактивный tab; при удалении активного выбирается соседний.

## Критерии приёмки

- [x] Familiar-табы используют серые semantic colors текущей темы: `Surface`/`Raised`, отличаясь от main selection.
- [x] Main-таб рендерится без `×`, familiar-табы — с `×` справа; полная ширина familiar-tab = `len(name) + 4`.
- [x] Клик на `×` в familiar-табе → модальное overlay «Close familiar `<name>`? [Y]es / [N]o».
- [x] `Y` → callback останавливает эмулятор, удаляет JSONL и запись `familiars.json`, tab исчезает, активный индекс корректируется.
- [x] `N` / `Esc` → overlay закрыт, состояние не меняется.
- [x] Клик вне overlay закрывает его без передачи события tab bar/терминалу.
- [x] Если `familiars.json` нет — закрытие работает (`RemoveFamiliar` = no-op).
- [x] Если JSONL фамильяра уже нет — ошибка `DeleteSessionJSONL` логируется, tab всё равно удаляется (best-effort cleanup).
- [x] UI regressions в `chat_panel_test.go` покрывают Unicode glyph, modal, activeIdx и Main-only PtyExit routing.
- [x] `TestRenderTabBarColorsFamiliarGrey` проверяет semantic серые цвета текущей темы.
- [x] `TestHandleMouseFamiliarCloseButtonTriggersConfirm` проверяет click в зоне `×`.
- [x] `TestConfirmYesDropsTabAndCallsCallback` проверяет подтверждённое закрытие.
- [x] `TestConfirmNoLeavesTabIntact` и `TestConfirmEscLeavesTabIntact` проверяют отмену.
- [x] `TestRemoveFamiliarUpdatesFile` и `TestRemoveFamiliarMissingFileNoOp` проверяют storage cleanup.
- [x] `go vet ./...` чисто.
- [x] `go test ./internal/...` зелёный.
- [x] `go build -o automata .` успешен.
- [x] Ручная проверка: открыть чат с 2+ фамильярами, кликнуть `×` → подтверждение → подтвердить → фамильяры закрыты, их JSONL удалены, в `familiars.json` их нет.

## Результаты проверки

- Unit: `internal/ui` и `internal/paths` проходят; покрыты glyph, theme, modal, cancel, activeIdx и Main-only PtyExit routing.
- Host integration: `TestCloseFamiliarCleansHostState` подтверждает удаление JSONL, запись `familiars.json`, cache и `activeSessions` с сохранением соседнего familiar.
- Integration: `go vet ./...`, `go test ./... -count=1 -p 1`, `go build -o automata .` проходят.
- Live cuTTY: два независимых запуска с двумя familiar tabs; оба tab-а закрыты через modal/Y, `N` проверен в отдельном smoke, реальные JSONL удалены и `familiars.json` стал `[]`.
