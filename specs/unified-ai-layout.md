# Unified AI data layout

## Контекст
Ранее Automata и `ai-knowledge` хранили данные в `~/.automata/`, а расширение
`pi` хранило данные отдельно. Это приводило к тому, что планы,
статус и заметки, созданные в `ai-knowledge` или в Automata, не видны в
других инструментах и не учитывают профиль. Задачи (tasks) устарели и
удалены из `ai-knowledge`; теперь используются только планы (plans).

## Цель
Перевести Automata, `ai-knowledge` и pi-расширения на единую файловую
структуру внутри `~/.ai/automata/`, чтобы все инструменты работали с одними
и теми же файлами и корректно изолировали данные по профилям.

## Итоговая структура

```
~/.ai/automata/
└── profiles/
    └── <profile-slug>/
        ├── state.json              # дерево Automata + tree_width, plan_width
        ├── sessions/
        │   └── <session-id>/
        │       ├── settings.json
        │       ├── plans.json
        │       └── status.json
        └── domains/
            └── <domain>/
                └── notes.json
```

- `<profile-slug>` — результат `slug.Slug(profile)`; профиль по умолчанию
  (`""`) → `default`.
- `<session-id>` — composed session ID без изменений, например
  `human-horizon__starframe.cue-tty.chat1`.
- `<domain>` — домен в том же формате, что и sessionID, но без имени чата,
  например `human-horizon__starframe.cue-tty`.

### state.json

```json
{
  "version": 1,
  "items": [...],
  "tree_width": 30,   // фиксированная ширина левой панели в символах
  "plan_width": 40    // фиксированная ширина правой план-панели в символах
}
```

`tree_width` и `plan_width` опциональны (omitempty). При отсутствии
используются значения по умолчанию: 30 для tree, 40 для plan.

## Миграция

При первом запуске Automata:
1. Если `~/.ai/automata/profiles/default/` не существует, но `~/.automata/`
   существует — скопировать содержимое `~/.automata/` в
   `~/.ai/automata/profiles/default/` рекурсивно.
2. Также мигрируются подпапки `~/.automata/<profile>/`, содержащие
   `state.json`, → `~/.ai/automata/profiles/<profile-slug>/`.
3. Не удалять `~/.automata/` — оставить как backup.
4. Все дальнейшие операции идут уже в `~/.ai/automata/`.

## Изменения в коде

### Automata
- Новый пакет `internal/paths` с функциями:
  - `BaseDir() string` → `~/.ai/automata` (или `$AI_DATA_HOME`).
  - `ProfileDir(profile string) string` → `~/.ai/automata/profiles/<slug>`.
  - `StatePath(profile string) string` → `~/.ai/automata/profiles/<slug>/state.json`.
  - `SessionsDir(profile string) string`.
  - `SessionDir(profile, sessionID string) string`.
  - `DomainsDir(profile string) string`.
  - `DomainDir(profile, domain string) string`.
- `internal/tree/state.go` использует `paths.StatePath`.
- `internal/tree/migrate.go` копирует legacy-данные.
- `main.go` передаёт `AI_PROFILE` и `AUTOMATA_SESSION_ID` в PTY.
- `internal/ui/context_panel.go` передаёт `AI_PROFILE`, `AI_SESSION_ID` и
  `AI_DOMAIN` при старте `ai-knowledge`.

### ai-knowledge (Go)
- Пути строятся через `~/.ai/automata/profiles/<slug>/`.
- `AI_PROFILE` (оригинальное имя) slugify-ится для директории.
- `AI_SESSION_ID` и `AI_DOMAIN` используются как есть.
- `settings.json` пишется в `profiles/<slug>/sessions/<session-id>/`.
- Tasks удалены; остались plans, status, settings, domain notes.

### ai-knowledge / job (Zed extensions)
- `dataHome()` → `~/.ai/automata` (или `$AI_DATA_HOME`).
- `profileSlug(sessionId)` берёт профиль из `$AI_PROFILE` или префикса
  sessionID до `__`.
- `sessionPath(sessionId)` → `profiles/<slug>/sessions/<session-id>`.
- `domainNotesPath(domain)` → `profiles/<slug>/domains/<domain>/notes.json`.
- Плагин `job` пишет `jobs/` и `status.json` в
  `profiles/<slug>/sessions/<session-id>/`.

## Контракт env для ai-knowledge
- `AI_PROFILE` — оригинальное имя профиля (не slug).
- `AI_SESSION_ID` — session ID.
- `AI_DOMAIN` — домен.
- `AI_DATA_HOME` — переопределение базовой папки (для тестов).

## Критерии приёмки
- [x] `automata` без `--profile` читает/пишет в
      `~/.ai/automata/profiles/default/`.
- [x] `automata --profile "Human Horizon"` читает/пишет в
      `~/.ai/automata/profiles/human-horizon/`.
- [x] `ai-knowledge -s <id>` читает/пишет в ту же папку сессии.
- [x] `ai-knowledge -d <domain>` читает/пишет в ту же папку домена.
- [x] Плагин `job` пишет jobs/status в профильный путь.
- [x] Tasks удалены из ai-knowledge и плагина; чекбоксы Auto/Dual в режиме
      планов взаимоисключающие.
- [x] Миграция из `~/.automata/` проходит автоматически.
- [x] Все e2e-тесты проходят.
- [x] `go vet ./...` и `go build -o automata .` чисты.
