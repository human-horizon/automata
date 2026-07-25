# Перенос ai-knowledge внутрь Automata и упрощение layout

## Контекст

Сейчас `ai-knowledge` — отдельный Go-модуль в `modules/ai-knowledge`, подключаемый к Automata через `replace` в `go.mod`. Интерфейс Automata уже использует панели `ContextPanel` (план) и `KnowledgePanel` (status/plans/jobs/notes). Пользователь считает переключение `F7` лишним: панель знаний должна быть видна всегда для чатов, а для папок — справа показывать контент/notes. Предпросмотр папки (`FolderPanel`) больше не нужен, потому что содержимое папки доступно через вложенные чаты/терминалы.

## Цель

1. Убрать отдельный модуль `modules/ai-knowledge`; перенести его код внутрь Automata.
2. Сделать Knowledge-панель постоянной для чатов.
3. Для папок показывать план/notes (`ContextPanel`) вместо `FolderPanel`.
4. Добавить индикатор `●` справа от папки, если внутри неё есть активные терминалы или чаты.

## Что изменится

1. `go.mod` — убрать `require github.com/HumanHorizon/ai-knowledge` и `replace ... => ./modules/ai-knowledge`.
2. `automata/internal/ai-knowledge/` — новый пакет с кодом из `modules/ai-knowledge/pkg/ui/`.
3. `automata/internal/ui/knowledge_panel.go` — обновить импорты на `internal/ai-knowledge/ui`.
4. `automata/internal/ui/context_panel.go` — без изменений, но `ai-knowledge` executable должен оставаться доступным (команда запускается как внешний бинарник).
5. `automata/internal/ui/container.go` — убрать `ChatWithPlanMode`, `FolderMode`, `FolderPanel`; оставить только `ChatMode` (чат + Knowledge) и `FolderMode` (ContextPanel для папки).
6. `automata/internal/ui/folder_preview.go` — удалить (логика предпросмотра больше не нужна).
7. `automata/main.go` — убрать обработку `F7` и `toggleKnowledge()`.
8. `automata/internal/tree/render.go` — добавить отрисовку `●` для папок, содержащих активные сессии.
9. `automata/internal/tree/model.go` — расширить API для получения статуса активных детей папки.
10. `automata/internal/tree/` — обновить тесты.
11. `automata/modules/ai-knowledge/` — удалить папку.

## Детали реализации

### 1. Перенос кода
- Скопировать `modules/ai-knowledge/pkg/ui/render.go` и `render_test.go` в `automata/internal/ai-knowledge/ui/`.
- Обновить package declaration на `package aiknowledge` или `package ui`.
- Обновить импорт в `automata/internal/ui/knowledge_panel.go` с `github.com/HumanHorizon/ai-knowledge/pkg/ui` на `github.com/HumanHorizon/automata/internal/ai-knowledge/ui`.
- Удалить `modules/ai-knowledge/`.
- Удалить `replace github.com/HumanHorizon/ai-knowledge => ./modules/ai-knowledge` из `go.mod`.

### 2. Упрощение Container
- Убрать константы `ChatWithPlanMode` и `FolderMode` (оставить только `ChatMode` и `FolderMode`? Переименовать для ясности).
- Переименовать `ChatMode` → `ChatMode` (оставить), но по умолчанию показывать KnowledgePanel.
- Убрать `planPanel`, `planWidth`, `planCollapsed`, `folderPanel`, `folderFraction`.
- `SetChat` — создавать `ChatPanel` + `KnowledgePanel` справа (KnowledgeMode).
- `SetFolder` — создавать `ContextPanel` с domain папки.
- `Active()` возвращать активный chat panel или context panel.
- `SetKnowledgeSession`, `RefreshKnowledge`, `SetKnowledge` удалить или упростить.

### 3. Domain для папки
- Domain = `item.EffectiveBoundPath()` если не пустой.
- Иначе domain = `slug.Slug(item.Path())` (или имя папки); уточнить с ai-knowledge conventions.

### 4. Индикатор активных сессий в папке
- `Tree` имеет `activeSessions map[string]struct{}`.
- Добавить метод `HasActiveDescendant(item *Item) bool`, который рекурсивно проверяет детей папки: если ребёнок — чат/терминал и его sessionID в `activeSessions`, или если ребёнок — папка и `HasActiveDescendant` true, вернуть true.
- В `renderItemLine` для `item.IsFolder && item.Expanded` (или всегда?) добавить `●` справа от имени, если `HasActiveDescendant`.
- Убедиться, что иконка не конфликтует с кнопками остановки/меню.

### 5. Убрать F7
- Удалить `case "f7"` в `App.Update`.
- Удалить `App.toggleKnowledge()`.
- `lastKnowledgeRefresh` больше не нужен? Или оставить для `RefreshKnowledge` внутри панели.

### 6. Тесты
- Обновить `container_test.go` (если есть) или e2e-тесты, ожидающие F7 / ChatWithPlan.
- Обновить `tree` тесты на новый статус папки.
- Добавить unit-тест `HasActiveDescendant`.

## Критерии приёмки

- [x] `modules/ai-knowledge` удалён, код живёт в `internal/ai-knowledge/ui`.
- [x] `go.mod` не содержит `replace` и `require` для `ai-knowledge`.
- [x] `go test -count=1 ./...` проходит.
- [x] `go vet ./...` проходит.
- [x] `go build -o automata .` проходит.
- [x] F7 больше не переключает панель; Knowledge панель всегда видна для чатов.
- [x] Для папок справа показывается ContextPanel с domain.
- [x] У папки справа рисуется `●`, если внутри есть активные терминалы/чаты.
- [x] E2E-тесты обновлены и проходят.
- [x] Коммит не создан.


## Результат

- Реализовано 2026-07-10.
- Все unit и e2e тесты проходят.
- Бинарник пересобран с `go build -o automata .`.
