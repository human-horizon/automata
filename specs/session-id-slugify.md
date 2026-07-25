# Slugify sessionID для pi-сессий Automata

## Контекст
SessionID для чатов и терминалов Automata должен быть уникальным, переносимым
и совместимым с `just-pi --session-id`. Ранее использовался префикс `pi-`,
что приводило к коллизиям и не учитывало путь папок.

## Цель
Сформировать уникальный sessionID из slug-имён папок в дереве и имени чата,
без привязки к cwd. Профиль, если задан, добавляется префиксом через `__`.

## Итоговый формат

```
<profile-slug>__<folder1>.<folder2>.<chat>
```

- `<profile-slug>` — `slug.Slug(profile)`; для профиля по умолчанию отсутствует.
- `<folderN>` — slug-имена папок от корня дерева до родителя чата.
- `<chat>` — slug-имя чата.
- Разделитель папок и чата — точка.
- Разделитель профиля и остальной части — `__`.

### Примеры
- Профиль `Human Horizon`, чат `My Chat` в папке `Today`:
  `human-horizon__today.mychat`
- Без профиля, чат `My Chat` в папке `Today`:
  `today.mychat`
- Корневой чат `Общие вопросы` в профиле `Human Horizon`:
  `human-horizon__obschie-voprosy`

## Что изменилось
1. `internal/slug/slug.go`:
   - `Slug(s string) string` — транслит, lowercase, нормализация спецсимволов.
   - `SessionName(folders []string, chatName string) string` — точечная
     склейка slug-имён папок и чата.
   - `ComposeSessionID(_ string, folders []string, chatName string) string` —
     делегирует `SessionName` (cwd больше не используется).
2. `main.go` — `SessionManager.openItem`:
   - `sessionName = slug.SessionName(item.Path(), item.Name)`.
   - Если профиль не пустой: `sessionName = slug.Slug(sm.Profile) + "__" + sessionName`.
   - `AUTOMATA_SESSION_ID = sessionName`.
   - `SetChat(sel, sessionName)` — план-панель получает полный sessionID с
     префиксом профиля.
3. `internal/tree/model.go` — `Item.Domain(profile)` возвращает домен в том же
   формате: `<profile-slug>__<folder1>.<folder2>`.

## Детали slugify
- Кириллица транслитерируется (`Проекты` → `proekty`).
- Пробелы, `_`, `/`, `\`, `.` и остальные не-буквенно-цифровые символы → `-`.
- Несколько `-` схлопываются в один, крайние `-` обрезаются.

## Тестирование
- `TestRootChatSessionID`: корневой чат → `cue-test-session__obschie-voprosy`.
- `TestFolderChatSessionID`: чат в папке → `cue-test-session-folder__segodnya.my-chat`.
- `TestUniqueSessionID`: одинаковые имена чатов в разных папках дают разные
  sessionID.

## Критерии приёмки
- [x] `go test ./...` — все PASS.
- [x] `go vet ./...` — чисто.
- [x] `go build -o automata .` — успешно.
- [x] SessionID не содержит cwd.
- [x] SessionID содержит префикс профиля через `__`.
- [x] План-панель и плагины получают тот же sessionID, что и `just-pi`.
