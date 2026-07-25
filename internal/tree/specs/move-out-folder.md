# Move Up / Move Down / Move Out для папок

## Контекст

В контекстном меню дерева для чатов уже есть пункты **Move up** / **Move down**
(см. `internal/tree/model.go:showContextMenu`). У папок этих команд нет.

Дополнительно нужна команда **Move out** — переместить элемент на уровень выше
(в родителя родителя или в корень, если родитель — корневой).

В текущем коде `showContextMenu` есть баг: после веток `if sel.IsFolder` и
`else if !sel.IsFolder` стоит **вторая** ветка `else if sel.IsFolder`,
которая недостижима. Вся её логика (Move up/down для папок + Bind/Reveal/Unbind)
сейчас мертва.

## Цель

1. Добавить в контекстное меню папки пункты: **Move up**, **Move down**, **Move out**.
2. Добавить метод `MoveSelectedOut()` на `Tree`.
3. Исправить логику веток в `showContextMenu`, чтобы Move up/down/bind работали
   и для папок (бывшая мёртвая ветка).
4. Сохранить существующие пункты (New Folder/Chat/Terminal, Rename, Archive/Unarchive,
   Delete, Bind/Reveal/Unbind) — порядок: New*, Rename, Archive, Move up, Move down,
   Move out, Bind-группа, Delete.

## Что изменится

1. `internal/tree/model.go`
   - Новый метод `(t *Tree) MoveSelectedOut() bool` — перемещает выбранный элемент
     после его родителя в `grandparent.Children` (или в `t.root`, если родитель
     корневой). Возвращает `true`, если перемещение произошло.
   - Рефакторинг `showContextMenu`: убрать дублирующую ветку; объединить так,
     чтобы папка получала и базовые пункты, и Move up/down, и Bind/Reveal/Unbind.

## Детали реализации

### `MoveSelectedOut()`

- `sel := t.ItemAt(t.selected)`.
- Если `sel == nil` или `sel.parent == nil` — `return false` (уже на верхнем уровне).
- `parent := sel.parent`, `grandparent := parent.parent`.
- Если `grandparent == nil` — новое место: `t.root` после `parent`.
- Удалить `sel` из `parent.Children`, вставить в
  `grandparent.Children`/`t.root` сразу после `parent`.
- Вернуть `true`. Сохранить существующую логику архивации при перемещении
  в корень (как в `moveItem`).
- Если родитель не существует в `grandparent.Children` (защита от рассинхрона) —
  `return false`.

### Контекстное меню папки

Объединённая структура (порядок важен):

1. New Folder
2. New Chat
3. New Terminal
4. Rename
5. Archive / Unarchive
6. Move up
7. Move down
8. Move out
9. Bind/Reveal/Unbind-группа (только если `sel.BoundPath != ""` → Reveal + Unbind,
   иначе → Bind to folder…).
10. Delete

Для чатов остаётся прежний порядок: Rename, Move up, Move down, Delete.

## Критерии приёмки

- [ ] `go vet ./...` — чисто.
- [ ] `go build ./...` — собирается.
- [ ] `go test ./internal/tree/...` — проходит.
- [ ] Новый тест `TestMoveSelectedOutFromNestedFolder` — папка внутри папки
      перемещается на уровень выше после `MoveSelectedOut()`.
- [ ] Контекстное меню папки содержит пункты `Move up`, `Move down`, `Move out`
      (проверка через юнит-тест на формирование `items`).
