# Терминалы в папках

## Контекст
В Automata уже есть папки и чаты (pi-сессии через `just-pi`). Пользователю нужен третий тип элемента — **терминал**, который запускает обычный shell в контексте папки, без привязки к `just-pi`.

## Цель
Добавить в дерево элементы типа Terminal. Они открывают shell-PTY с уникальным session-id, основанным на пути папки и имени терминала.

## Поведение

### В дереве
- Новый тип `Item.IsTerminal`.
- Иконка: `>_`.
- Может находиться в корне или внутри папок.
- Поддерживаются те же действия, что и для чатов: rename, delete, drag & drop.

### Создание
- В контекстном меню папки добавляется пункт **"New Terminal"**.
- В контекстном меню корня (правый клик на пустом месте) тоже **"New Terminal"**.
- Открывается input modal с prompt `Terminal name:`.

### Открытие
- Enter / левый клик по терминалу открывает его в правой панели.
- Запускается shell (`PI_CMD` env или `term.DefaultShell()`).
- `AUTOMATA_SESSION_ID` формируется как `slug.ComposeSessionID(cwd, item.Path(), item.Name)` с префиксом профиля.
- Пример: папка `Today`, терминал `logs` → `human-horizon__users.a.space.projects.humanhorizon.automata.today.logs`.

### State persistence
- `StateItem` получает поле `IsTerminal bool`.
- Загрузка/сохранение сохраняет терминалы.

## UI
- Заголовок шапки остаётся прежним: `+Folder`, `+Chat`.
- В контекстном меню папки: `New Folder`, `New Chat`, `New Terminal`, `Rename`, `Delete`.
- В контекстном меню чата/терминала: `Rename`, `Delete`.

## Реализация
1. `internal/tree/model.go`: добавить `IsTerminal` в `Item`, `StateItem`.
2. `internal/tree/model.go`: методы `addChildTerminal`, `AddTerminal`, `NewTerminal` с уникальным именем.
3. `internal/tree/input.go`: обработка input modal для `Terminal name:`.
4. `internal/tree/model.go`: контекстные меню — добавить `New Terminal`.
5. `main.go`: `SessionManager.openChat` → `openItem`, которое понимает `IsTerminal` и запускает shell вместо `just-pi`.
6. `internal/slug`: убедиться, что session-id корректный.

## Критерии приёмки
- [ ] Можно создать терминал в папке через правый клик.
- [ ] Можно создать терминал в корне.
- [ ] Терминал открывается и показывает shell prompt.
- [ ] У терминала уникальная pi-сессия на основе пути.
- [ ] Терминал сохраняется между запусками.
- [ ] Rename/delete/drag работают.
- [ ] `go test ./...`, `go vet ./...`, `go build` проходят.
