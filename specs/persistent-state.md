# Персистентное состояние дерева

## Контекст
Сейчас дерево чатов/папок создаётся заново при каждом запуске Automata — три хардкодных элемента в `main.go`. 
Изменения (добавление, удаление, переименование) теряются после выхода.

Нужно сохранять состояние дерева в JSON-файл `~/.automata/state.json` и восстанавливать при старте.

## Цель
Автоматически сохранять всё дерево в `~/.automata/state.json` при каждом изменении и загружать при старте, 
чтобы структура папок/чатов переживала перезапуск приложения.

## Что изменится

1. `internal/tree/state.go` — новый файл: структура `TreeState` (JSON), функции `Save()` / `Load()`
2. `internal/tree/model.go` — после каждой мутации вызывать автосохранение; метод `LoadFromState()`
3. `main.go` — убрать хардкодные элементы, загружать из файла при старте; если файла нет — создавать дефолтные

## Детали реализации

### 1. Формат JSON (`~/.automata/state.json`)

```json
{
  "version": 1,
  "items": [
    { "name": "Сегодня", "is_folder": true, "expanded": true, "items": [] },
    { "name": "Общие вопросы", "is_folder": false, "items": null },
    { "name": "Проекты", "is_folder": true, "expanded": true, "items": [
      { "name": "Automata", "is_folder": true, "expanded": true, "items": [] }
    ]}
  ]
}
```

- `is_folder` → `IsFolder`.
- `items` — `null` для чатов, массив для папок.
- `version` — для будущей миграции.
- `ID` не сохраняется — генерируется при загрузке (UUID v4).
- Порядок элементов сохраняется.

### 2. `internal/tree/state.go`

```go
package tree

import (
    "encoding/json"
    "os"
    "path/filepath"
)

const stateFileName = ".automata/state.json"

type TreeState struct {
    Version int          `json:"version"`
    Items   []*StateItem `json:"items"`
}

type StateItem struct {
    Name     string       `json:"name"`
    IsFolder bool         `json:"is_folder"`
    Expanded bool         `json:"expanded,omitempty"`
    Items    []*StateItem `json:"items,omitempty"` // nil для чатов, [] для папок
}
```

Функции:

```go
func stateFilePath() (string, error)
// Возвращает ~/.automata/state.json, создаёт ~/.automata если не существует.

func (t *Tree) SaveState() error
// Сериализует t.root в TreeState → JSON → stateFilePath()

func (t *Tree) LoadState() error
// Читает stateFilePath(), десериализует, заменяет t.root и перестраивает flat-список

func (t *Tree) ClearState() error
// Удаляет файл состояния (опционально — для тестов и ручной очистки)
```

Сериализация: рекурсивный обход `[]*Item` → `[]*StateItem`.
Десериализация: рекурсивный обход `[]*StateItem` → `[]*Item` с генерацией UUID для `Item.ID`.

Импорт UUID: `github.com/google/uuid` — уже косвенная зависимость (bubbletea использует).

### 3. `internal/tree/model.go` — автосохранение

Добавить метод `autoSave()`:

```go
func (t *Tree) autoSave() {
    if err := t.SaveState(); err != nil {
        // Логируем, но не падаем — ошибка записи не должна ломать приложение
        fmt.Fprintf(os.Stderr, "Failed to save tree state: %v\n", err)
    }
}
```

Вызывать `autoSave()` после каждой мутации:
- `AddFolder` / `AddChat`
- `addChildFolder` / `addChildChat`
- `renameItem`
- `deleteItem`
- `moveItem`
- `ToggleFolder`

### 4. `main.go` — загрузка при старте

Заменить:

```go
t := tree.New()
t.AddFolder("Сегодня")
t.AddChat("Общие вопросы")
t.AddFolder("Проекты")
```

На:

```go
t := tree.New()
_ = t.LoadState() // Если файла нет — пустое дерево, не ошибка
```

### 5. UUID генерация

Добавить в `model.go`:

```go
import "github.com/google/uuid"

func generateID() string {
    return uuid.New().String()
}
```

При создании `Item` (как через API, так и через десериализацию) — вызывать `generateID()`.

Сейчас `ID` проставляется в `AddFolder`/`AddChat` как `ID: name`. Нужно переделать на UUID.
- `New()` → не трогаем
- `AddFolder` / `AddChat`: `ID: generateID()`
- `addChildFolder` / `addChildChat`: `ID: generateID()`
- Десериализация `StateItem` → `Item`: `ID: generateID()`

### 6. Обработка ошибок
- Если файла `~/.automata/state.json` нет при загрузке — не ошибка, пустое дерево
- Если файл повреждён (невалидный JSON) — не падаем, пустое дерево
- Если не удаётся создать `~/.automata/` директорию — не падаем, продолжаем без файла
- Ошибки сохранения — только stderr, не влияют на работу

Файл создаётся только при первом сохранении (когда пользователь что-то меняет).

## Критерии приёмки
- [ ] `SaveState()` сохраняет дерево в `~/.automata/state.json` в читаемом JSON
- [ ] `LoadState()` восстанавливает дерево из файла
- [ ] После создания папки/чата через UI — изменения сохраняются
- [ ] После перезапуска app — дерево восстанавливается
- [ ] Если файла нет — пустое дерево, без ошибок
- [ ] Если файл повреждён — пустое дерево, приложение не падает
- [ ] UUID генерируется для каждого элемента при создании
- [ ] `go build ./...` ✅, `go test ./...` ✅, `go vet ./...` ✅