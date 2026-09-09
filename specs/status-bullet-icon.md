# Единый формат индикатора статуса: кружочек ●

## Контекст

Сейчас в правой панели Automata (KnowledgePanel) `actionIcon` рисует индивидуальные символы для каждого действия: `~ thinking`, `R read`, `W write`, `G grep`, `> run`, `X stop`, `A analyze`, `i status`, `J job`. В левой панели (Tree) для всех тех же действий рисуется `●` + слово (`● thinking`, `● read`, `● write`...), а для idle — `○ idle` (см. `internal/tree/render.go:statusEmojiToSpec`).

Из-за этого два места показывают один и тот же статус в разном формате и пользователю приходится дважды парсить визуальный язык. Нужно унифицировать: везде `●` + каноническое слово действия, idle остаётся пустым (как сейчас в правой панели), чтобы не дублировать `○ idle` из Tree.

## Цель

`actionIcon` в `internal/ai-knowledge/ui/render.go` возвращает `● ` + каноническое слово для всех известных действий и пустую строку для `idle`. Канонические слова согласуются с `internal/tree/render.go:statusEmojiToWord` где возможно.

## Что изменится

1. `internal/ai-knowledge/ui/render.go` — переписать `actionIcon` на единый `● ` + word.
2. `internal/ai-knowledge/ui/render_test.go` — обновить `TestActionIcon` под новые ожидания и добавить кейсы для `wait`/`active`/`status`.
3. `CONTEXT.md` — запись проблемы/решения.

## Детали реализации

Маппинг (согласован с `tree/statusEmojiToWord` где возможно):

| action      | сейчас            | станет            |
|-------------|-------------------|-------------------|
| `thinking`  | `~ thinking`      | `● thinking`      |
| `read`      | `R <description>` | `● read`          |
| `write`     | `W <description>` | `● write`         |
| `grep`      | `G <description>` | `● grep`          |
| `find`      | (default)         | `● find`          |
| `analyze`   | `A <description>` | `● analyze`       |
| `wait`      | (default)         | `● wait`          |
| `job`       | (default)         | `● job`           |
| `run`       | `> <description>` | `● run`           |
| `status`    | `i <description>` | `● status`        |
| `active`    | `A <description>` | `● active`        |
| `stop`      | `X <description>` | `● stopped`       |
| `idle`      | `""`              | `""`              |
| default     | `<action> <description>` | `● <action>` |

Правила:
- Description больше не подмешивается в main-строку — он и так рендерится отдельной строкой ниже через `pathStyle` в `View` (см. `render.go:64-69`). Это устраняет дублирование и укорачивает main.
- `stopped` (а не `stop`) — чтобы совпадать с `tree/statusEmojiToWord`, где ключ `X` → `stopped`.
- `find`/`wait`/`job` явно добавлены в switch, чтобы не падать в default с сырым action.
- Idle остаётся пустым, чтобы не дублировать `○ idle` из Tree. Семантика: Tree показывает "неактивный" индикатор, KnowledgePanel показывает "активный" — они дополняют друг друга, а не повторяют.

## Критерии приёмки

- [x] `actionIcon` возвращает `● thinking`, `● read`, `● write`, `● grep`, `● find`, `● analyze`, `● wait`, `● job`, `● run`, `● status`, `● active`, `● stopped` для соответствующих action.
- [x] `actionIcon("idle", "")` возвращает `""`.
- [x] `actionIcon` больше не подмешивает description в main-строку.
- [x] `TestActionIcon` обновлён, все кейсы зелёные.
- [x] `gofmt`, `go vet ./...`, `go test ./...` зелёные.
- [x] Бинарник `automata` пересобран `go build -o automata .`.
