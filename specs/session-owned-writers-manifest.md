# Manifest для всех Automata-owned session writers

## Цель

Каждая новая директория `sessions/<sessionID>` получает manifest v1 до записи session-owned данных. Существующая директория без manifest остаётся schema v0: автоматическая миграция запрещена.

## Требования

- Инвентаризировать все production writers под `paths.SessionDir(profile, sessionID)` и все вызовы, создающие такие директории.
- Для writer, который способен создать новую session directory, вызвать `paths.EnsureSessionDir(profile, sessionID)` перед записью.
- Сохранить atomic write и private permissions уже существующих файлов.
- Не создавать manifest для legacy-директории только потому, что её прочитали/обновили; `EnsureSessionDir` должен сохранить установленную семантику v0.
- Rename/move переносит существующий manifest вместе с каталогом и не меняет schema автоматически.

## Известные точки аудита

- `writeTaskToChatStatus` и `writeTaskRemovedFromChat` в Kanban создают `status.json` через общий atomic writer.
- `KnowledgePanel.writeSettings` записывает session-owned `settings.json`.
- Дополнительно проверить writers в `internal/paths`, main lifecycle, Kanban rollback/restore, watcher/setup и прямые `os.WriteFile`/`atomicfile.Write` обращения.

## Проверки приёмки

- Каждый проверенный writer, вызванный с отсутствующей session directory, создаёт manifest v1.
- Запись в существующую legacy directory без manifest не добавляет manifest и сохраняет данные.
- Rename/move переносит manifest; rollback оставляет прежний manifest и данные целыми.
- Статический повторный поиск session-owned write paths не обнаруживает необёрнутых writers.
- Тесты `internal/paths`, `internal/ui`, `main` и `go test -race ./...` проходят.
