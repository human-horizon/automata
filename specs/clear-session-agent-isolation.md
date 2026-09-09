# Clear: изоляция агентов — каждый профиль удаляет только свои JSONL

## Контекст

`specs/multi-pi-session-roots.md` (от 13.08) расширил `paths.SessionRoots()` чтобы возвращать все `~/.ai/<agent>/pi/sessions/`. Это решает «Clear не находит JSONL» — но **создаёт новую регрессию безопасности**:

> Сценарий: Automata Getic запущена с `--profile Getic --pi getic`. Пользователь нажимает Clear в чате Getic с sessionID `xyz` и cwd `/Users/a/Space`. До фикса `FindSessionJSONL(xyz, "/Users/a/Space")` искал только в `~/.ai/just/pi/sessions/`, ничего не находил, ошибка. После фикса `FindSessionJSONL` ищет во **всех** корнях и **может** найти чужую сессию HumanHorizon с тем же sessionID в `~/.ai/just/pi/sessions/--Users-a-Space--/` — например, после миграции профиля или просто при коллизии имён. Затем `DeleteSessionJSONL` удаляет её. HumanHorizon теряет историю.

Аня подтвердила: «очень важно, чтобы учитывался параметр --pi при нажатии clear».

Корень: `SessionRoots()` слишком широк. Нужно ограничить Clear одним конкретным агентом — тем, в котором запущена Automata.

## Цель

Каждый профиль Automata удаляет JSONL **только** в своём pi-агенте. Кросс-агентное удаление невозможно, даже если sessionID совпадает.

## Что изменится

`internal/paths/sessionfile.go`:

1. **`FindSessionJSONL`** — сменить сигнатуру:
   ```go
   func FindSessionJSONL(sessionID, cwd, agentDir string) string
   ```
   - если `agentDir == ""` → fallback на текущее поведение (все корни, dynamic discovery);
   - если `agentDir != ""` → искать **только** в `filepath.Join(agentDir, "sessions")`.

2. **`DeleteSessionJSONL`** — то же самое:
   ```go
   func DeleteSessionJSONL(sessionID, cwd, agentDir string) (string, error)
   ```

3. **`SessionRoots`** — оставить без изменений для fallback-варианта.

`internal/main.go::clearSessionCmd`:

4. Все вызовы `FindSessionJSONL` / `DeleteSessionJSONL` теперь передают `a.piAgentDir` (полный путь вида `/Users/a/.ai/<tag>/pi`).

5. **Fail-safe**: если `a.piAgentDir == ""` (старая сборка или ошибка), Clear **логирует** `clearSession: refuse to clear without piAgentDir (sid=%q)` и **возвращает nil** без удаления. Это защита: лучше не удалить, чем удалить чужое.

`internal/paths/sessionfile_test.go`:

6. Все существующие вызовы `FindSessionJSONL` / `DeleteSessionJSONL` обновить — добавить `""` третьим аргументом (fallback на dynamic roots, поведение как до фикса multi-pi).

7. `writeSessionFixture` принимает дополнительный параметр `agentDir` — `writeSessionFixture(t, home, cwd, agentDir, sessionID, modTime)`. По умолчанию пишет в `~/.ai/just/pi/sessions/` (обратная совместимость со старыми вызовами через обёртку).

8. Новые тесты:
   - `TestFindSessionJSONLRespectsAgentFilter` — JSONL в `~/.ai/just/pi/sessions/` с sessionID `xyz` существует. Вызов `FindSessionJSONL("xyz", "/cwd", "/Users/a/.ai/getic/pi")` возвращает `""` (агент-фильтр отрезает just). Вызов с пустым agentDir возвращает путь.
   - `TestDeleteSessionJSONLRefusesCrossAgent` — JSONL в `just/` существует. `DeleteSessionJSONL("xyz", "/cwd", "/Users/a/.ai/getic/pi")` возвращает ошибку `session file not found for "xyz" in agent ...` и **не трогает** файл в just/. Проверка `os.Stat` показывает что файл на месте.
   - `TestClearSessionCmdRefusesWithoutPiAgentDir` (в `main_view_test.go`) — `App.piAgentDir = ""`. После вызова `clearSessionCmd` JSONL остаётся на месте, лог содержит «refuse to clear».

## Детали реализации

1. `FindSessionJSONL`:
   ```go
   func FindSessionJSONL(sessionID, cwd, agentDir string) string {
       if sessionID == "" {
           return ""
       }
       roots := sessionRootsFor(agentDir)  // см. ниже
       
       subdir := EncodeCwdDir(cwd)
       preferred := sessionFileCandidate{}
       for _, root := range roots {
           candidate, ok := newestSessionFile(filepath.Join(root, subdir), sessionID)
           if ok && candidate.newerThan(preferred) {
               preferred = candidate
           }
       }
       if preferred.path != "" {
           return preferred.path
       }
       
       fallback := sessionFileCandidate{}
       for _, root := range roots {
           entries, err := os.ReadDir(root)
           if err != nil {
               continue
           }
           for _, entry := range entries {
               if !entry.IsDir() || entry.Name() == subdir {
                   continue
               }
               candidate, ok := newestSessionFile(filepath.Join(root, entry.Name()), sessionID)
               if ok && candidate.newerThan(fallback) {
                   fallback = candidate
               }
           }
       }
       return fallback.path
   }
   
   func sessionRootsFor(agentDir string) []string {
       if agentDir == "" {
           return SessionRoots()
       }
       return []string{filepath.Join(agentDir, "sessions")}
   }
   ```

2. `DeleteSessionJSONL`:
   ```go
   func DeleteSessionJSONL(sessionID, cwd, agentDir string) (string, error) {
       if agentDir == "" {
           return "", fmt.Errorf("agentDir is required for safe deletion")
       }
       p := FindSessionJSONL(sessionID, cwd, agentDir)
       if p == "" {
           return "", fmt.Errorf("session file not found for %q in agent %q", sessionID, agentDir)
       }
       if err := os.Remove(p); err != nil {
           return "", err
       }
       return p, nil
   }
   ```
   Заметь: `agentDir == ""` теперь **ошибка**, а не «искать везде». Это fail-safe для Clear.

3. `clearSessionCmd`:
   ```go
   func (a *App) clearSessionCmd(sessionID, cwd string, familiarSIDs []string) tea.Cmd {
       return func() tea.Msg {
           agentDir := a.piAgentDir
           // ... kill familiars ...
           if agentDir == "" {
               log.Printf("clearSession: refuse to clear %q without piAgentDir", sessionID)
               return nil
           }
           // ... existing code ...
           deleted, err := paths.DeleteSessionJSONL(sessionID, cwd, agentDir)
           // ...
       }
   }
   ```

## Критерии приёмки

- [ ] `FindSessionJSONL(sid, cwd, agentDir)` ищет только в `<agentDir>/sessions/` когда `agentDir != ""`.
- [ ] `FindSessionJSONL(sid, cwd, "")` сохраняет старое поведение (все roots, dynamic discovery).
- [ ] `DeleteSessionJSONL(sid, cwd, "")` возвращает ошибку «agentDir required».
- [ ] `DeleteSessionJSONL(sid, cwd, agentDir)` не трогает JSONL в других агентах.
- [ ] `clearSessionCmd` передаёт `a.piAgentDir` и fail-safe при пустом.
- [ ] Существующие тесты `sessionfile_test.go` обновлены и проходят.
- [ ] 3 новых теста добавлены и проходят.
- [ ] `go vet ./...` чисто.
- [ ] `go test ./internal/...` зелёный.
- [ ] `go build -o automata .` успешен.
- [ ] **Ручная проверка**: перезапустить Getic Automata, нажать Clear в Getic чате — JSONL удаляется. Перезапустить HumanHorizon Automata, нажать Clear в HumanHorizon чате — JSONL удаляется (не трогает Getic).
- [ ] **Ручная проверка безопасности**: создать JSONL в `~/.ai/just/pi/sessions/` с тем же sessionID что в Getic, открыть Getic Automata, нажать Clear — JSONL в `just/` **не должен** удалиться.
