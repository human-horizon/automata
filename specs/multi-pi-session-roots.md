# Multi-pi session roots: пути ко всем pi-агентам

## Контекст

`paths.SessionRoots()` возвращает **только** `~/.ai/just/pi/sessions/`:

```go
func SessionRoots() []string {
    home, _ := os.UserHomeDir()
    ...
    return []string{filepath.Join(home, ".ai", "just", "pi", "sessions")}
}
```

Это работает для чатов, запущенных через стандартный `just-pi` (HumanHorizon, Command). Но у Ани несколько **разных pi-агентов**, каждый со своим каталогом sessions:

```
~/.ai/just/pi/sessions/    — 108 JSONL (just-pi default)
~/.ai/getic/pi/sessions/   — 15 JSONL (--pi getic)
~/.ai/synth/pi/sessions/   — 7 JSONL (--pi synth)
~/.ai/ask/pi/sessions/     — 0 (только что переименовано, пустая)
~/.ai/vexa/pi/sessions/    — 0 (пустая)
~/.ai/weft/pi/sessions/    — 0 (пустая)
```

Симптомы: в Getic `Clear` гасит чат (эмулятор убит), но `DeleteSessionJSONL` возвращает ошибку `session file not found`, JSONL остаётся. То же самое для Synth.

`FindSessionJSONL` использует fallback — ищет в **каждом** `SessionRoots()` подкаталоге, потом по всем подкаталогам. Если расширить `SessionRoots()` — фикс автоматический: fallback найдёт файл в нужном агенте.

## Цель

`SessionRoots()` возвращает список всех существующих каталогов `~/.ai/<agent>/pi/sessions/` на машине. Поиск сессий работает для всех pi-агентов одинаково.

## Что изменится

`internal/paths/sessionfile.go::SessionRoots`:

```go
// SessionRoots returns all ~/.ai/<agent>/pi/sessions/ directories that
// currently exist on this machine. Each just-pi-style agent (just, getic,
// synth, vexa, weft, ask, …) keeps its sessions under its own pi/ subdir.
// We discover them dynamically so a new agent works without code changes.
//
// Falls back to ~/.ai/just/pi/sessions/ when ~/.ai itself is missing.
func SessionRoots() []string {
    home, _ := os.UserHomeDir()
    if home == "" {
        home = "/Users/a"
    }
    base := filepath.Join(home, ".ai")
    entries, err := os.ReadDir(base)
    if err != nil {
        return []string{filepath.Join(base, "just", "pi", "sessions")}
    }
    var roots []string
    seen := make(map[string]bool)
    for _, e := range entries {
        if !e.IsDir() {
            continue
        }
        sessionsDir := filepath.Join(base, e.Name(), "pi", "sessions")
        if info, err := os.Stat(sessionsDir); err == nil && info.IsDir() {
            if !seen[sessionsDir] {
                roots = append(roots, sessionsDir)
                seen[sessionsDir] = true
            }
        }
    }
    if len(roots) == 0 {
        return []string{filepath.Join(base, "just", "pi", "sessions")}
    }
    return roots
}
```

## Детали

1. **Порядок**: каталоги возвращаются в том порядке, в котором `os.ReadDir` их отдаёт (лексикографический для типичных ФС). Это нормально, потому что `FindSessionJSONL` использует fallback — если предпочтительный cwd-кодированный подкаталог не нашёл файл, ищет по **всем** корням.

2. **Идемпотентность**: `seen` исключает дубликаты, если кто-то создал симлинк `~/.ai/x/pi/sessions → ~/.ai/y/pi/sessions`.

3. **Fallback**: если `~/.ai` не существует или пуст, возвращаем старый дефолт `just/pi/sessions`. Это сохраняет поведение «если ничего нет — пусть будет хоть что-то для тестов».

4. **Скрытые директории**: `os.ReadDir` отдаёт и dotfiles, но они не содержат `pi/sessions`, поэтому автоматически отфильтруются через `os.Stat`.

5. **`SessionRoots` — экспортируемая**: её вызывают `FindSessionJSONL` и потенциально другие пути в будущем. Изменение покрывает всех потребителей.

## Критерии приёмки

- [ ] `SessionRoots()` возвращает все существующие `~/.ai/<x>/pi/sessions/`.
- [ ] Если ни одного не существует, fallback на `just/pi/sessions`.
- [ ] Существующие тесты `TestDeleteSessionJSONLFallsBackAndDeletesOnlyExactMatch` и прочие в `internal/paths/sessionfile_test.go` продолжают проходить.
- [ ] Новый регрессионный тест `TestSessionRootsDiscoversAllAgents`: создаёт `t.TempDir()` с `home/.ai/{just,getic,synth}/pi/sessions/`, подменяет `HOME` через `t.Setenv`, проверяет что `SessionRoots()` возвращает все три (порядок может быть любым).
- [ ] Новый регрессионный тест `TestFindSessionJSONLLocatesAcrossAgents`: создаёт `home/.ai/getic/pi/sessions/--cwd/getic-...jsonl` с id `getic__foo`, вызывает `FindSessionJSONL("getic__foo", "/anywhere")`, проверяет что путь найден. До фикса возвращает "", после — корректный путь.
- [ ] `go vet ./...` чисто.
- [ ] `go test ./internal/...` зелёный.
- [ ] `go build -o automata .` успешен.
- [ ] Ручная проверка в живом Getic Automata: нажать Clear в любом чате Getic → JSONL удаляется.
