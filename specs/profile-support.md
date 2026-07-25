# Поддержка профилей в Automata

## Контекст
Пользователь работает с несколькими изолированными контекстами (например,
`ai`, `Human Horizon`) и хочет, чтобы каждый профиль имел своё дерево чатов,
свои pi-сессии и изолированные данные `ai-knowledge`. Профиль передаётся через
флаг `--profile <name>`.

## Цель
Реализовать полную изоляцию состояния, session-id и доменов на основе
профиля. Имя профиля может содержать пробелы и заглавные буквы, поэтому для
путей и префиксов оно всегда нормализуется через `slug.Slug`.

## Поведение

### CLI
```bash
automata --profile "Human Horizon"
```

### State isolation
- Профиль по умолчанию (`""`) хранит состояние в
  `~/.ai/automata/profiles/default/state.json`.
- Профиль `Human Horizon` хранит состояние в
  `~/.ai/automata/profiles/human-horizon/state.json`.
- Папка создаётся при первом сохранении.
- Профиль никогда не используется «как есть» в пути — всегда `slug.Slug(profile)`.
- Старые данные из `~/.automata/` мигрируются в
  `~/.ai/automata/profiles/default/` (или `profiles/<profile>/`, если были
  подпапки профилей) автоматически и неразрушающе.

### UI
- Заголовок дерева: ` Automata (Human Horizon) ` — оригинальное имя профиля.
- Если профиль не задан — ` Automata `.

### Session ID
- Базовое имя сессии формируется через `slug.SessionName(folders, chatName)`.
- К имени добавляется префикс: `slug.Slug(profile) + "__" + sessionName`.
- Пример: профиль `Human Horizon`, чат `My Chat` в папке `Today` →
  `human-horizon__today.mychat`.

### Domain
- Домен для заметок папки формируется так же, как sessionID, но без имени
  чата: `human-horizon__today`.
- `Item.Domain(profile)` в `internal/tree/model.go` отвечает за это
  формирование.

## Реализация
- `main.go`: `--profile` flag, `SessionManager.Profile`, `Tree.Profile`.
- `internal/paths/paths.go`: единые функции для путей
  `~/.ai/automata/profiles/<slug>/...`.
- `internal/tree/state.go`: `StatePath` через `paths.StatePath` + миграция из
  `~/.automata/`.
- `internal/tree/render.go`: заголовок окна показывает оригинальное имя
  профиля.
- `main.go`: `sessionName = slug.Slug(sm.Profile) + "__" + sessionName` и
  передача `AI_PROFILE` / `AUTOMATA_SESSION_ID` в PTY.

## Критерии приёмки
- [x] `--profile "Human Horizon"` использует папку
      `~/.ai/automata/profiles/human-horizon/`.
- [x] Дерево в профиле независимо от дефолтного.
- [x] Заголовок показывает оригинальное имя профиля.
- [x] Session-id содержит префикс `human-horizon__`.
- [x] Домены папок содержат префикс `human-horizon__`.
- [x] Миграция из `~/.automata/` проходит автоматически.
- [x] `go test ./...`, `go vet ./...`, `go build` проходят.
