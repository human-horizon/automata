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

### Explicit и legacy profile contracts
- Explicit API с пустым `profile` использует `profiles/default`; переменная
  `AI_PROFILE` не может переопределить явный аргумент.
- Legacy API, которому передан только `sessionID`, использует общий
  filesystem-aware resolver. Разделитель `__` сам по себе не доказывает, что
  префикс является профилем: тот же разделитель встречается в familiar IDs,
  например `chat__expert`.
- При нескольких существующих candidate directories read-only wrappers
  сохраняют legacy priority `prefix → AI_PROFILE → default`; destructive
  wrappers `KillSession` и `PruneStaleSession` завершаются ошибкой ambiguity
  до destructive side effects.

#### Контракт фазы 1
- `internal/ai-knowledge/memory` и `internal/ui/ContextPanel` разрешают
  каталоги доменов только через каноническую `paths.DomainDir`.
- `internal/ui/Container.RefreshKnowledgeCmd` должен использовать этот контракт;
  проверка `profile != ""` не должна пропускать чтение памяти профиля по
  умолчанию.
- Область реализации: `internal/ui/container.go`.
- Явно переданный пустой профиль (`profile == ""`) всегда означает
  канонический `default` и не разрешается через окружение.
- Только legacy convenience APIs используют `AI_PROFILE` как fallback. Они
  проверяют каталоги `profiles/<candidate>/sessions/<sessionID>` для кандидатов
  `profile-prefix → AI_PROFILE → default`; при отсутствии каталогов сохраняют
  этот же fallback-порядок. Для read-only неоднозначности применяется порядок
  кандидатов, а destructive legacy API при нескольких существующих каталогах
  возвращает ошибку неоднозначности до чтения/мутации job records и сигналов.
- Приёмка должна покрывать профиль с Unicode/кириллицей, пользовательский
  `AI_DATA_HOME` и путь watcher'а; во всех случаях используется
  канонический каталог домена.
- В фазу 1 не входят `TypeScript extension`, миграция, `AUTOMATA_HOME`,
  F5/F7 и диагностика мыши в `/tmp`.

## Реализация
- `main.go`: `--profile` flag, `SessionManager.Profile`, `Tree.Profile`.
- `internal/paths/paths.go`: единые функции для путей
  `~/.ai/automata/profiles/<slug>/...`.
- `internal/tree/state.go`: `StatePath` через `paths.StatePath` + миграция из
  `~/.automata/`.
- `internal/tree/render.go`: заголовок окна показывает оригинальное имя
  профиля.
- `main.go`: `sessionName = slug.Slug(sm.Profile) + "__" + sessionName`; `piLaunch`
  передаёт Pi child canonical `AI_PROFILE` и `AUTOMATA_PROFILE` (для пустого
  профиля обе переменные равны `default`) после inherited environment, поэтому
  они перекрывают конфликтующие значения родителя. `PI_CMD` меняет executable,
  но не profile environment.
- Portalis устанавливает `AUTOMATA_SESSION_ID` из `Emulator.SessionID`; этот
  session identity не заменяет profile variables.

## Критерии приёмки
- [x] `--profile "Human Horizon"` использует папку
      `~/.ai/automata/profiles/human-horizon/`.
- [x] Дерево в профиле независимо от дефолтного.
- [x] Заголовок показывает оригинальное имя профиля.
- [x] Session-id содержит префикс `human-horizon__`.
- [x] Домены папок содержат префикс `human-horizon__`.
- [x] Миграция из `~/.automata/` проходит автоматически.
- [x] `go test ./...`, `go vet ./...`, `go build` проходят.
