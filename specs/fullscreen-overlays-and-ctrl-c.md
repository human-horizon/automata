# Полноэкранные Help/Settings и выход по Ctrl+C

## Контекст

`Help` и `Settings` сейчас открываются внутри панели дерева, поэтому их overlay ограничен шириной левой панели. Глобальная обработка `Ctrl+C` выполняет `tea.Quit` только при фокусе дерева; в чате или терминале клавиша передаётся дочерней панели и Automata не закрывается.

## Цель

Открывать Help и Settings на корневом viewport всей Automata, сохранив управление мышью/клавиатурой и live-выбор темы. `Ctrl+C` должен завершать Automata независимо от текущего фокуса или открытого overlay.

## Что изменится

1. `main.go` и новый `main_overlay.go` — корневые Help/Settings overlays, маршрутизация событий и рендеринг поверх всего viewport.
2. `internal/tree/model.go`, `internal/tree/input.go` — callbacks для открытия Help/Settings из footer с fallback для изолированных unit-тестов.
3. `internal/tree/keyboard_test.go`, `main_test.go` и E2E-тесты — регрессии размера overlay и Ctrl+C.
4. `specs/keyboard-navigation-and-root-sort.md`, `CONTEXT.md` — актуализация поведения.

## Детали реализации

1. App хранит корневой `warp.Modal` для Help и `warp.Popover` для Settings.
2. App.View накладывает активный overlay на полный вывод `a.warp.View()` с размерами `a.warp.Width()`/`a.warp.Height()`, а не на вывод Tree.
3. App.Update обрабатывает overlay-клавиши и overlay-мышь до передачи событий Warp/панелям.
4. Footer Tree вызывает App callbacks; прямое использование Tree остаётся совместимым через текущий fallback.
5. Проверка `Ctrl+C` выполняется до focus-router и всегда возвращает `tea.Quit`; Ctrl/Alt/Tab остальных клавиш сохраняют прежнюю передачу в чат/терминал.

## Критерии приёмки

- [x] Help визуально центрирован относительно всей Automata, а не только Tree.
- [x] Settings визуально открывается поверх всей Automata и позволяет выбрать тему live.
- [x] Overlay закрывается штатными клавишами/мышью.
- [x] `Ctrl+C` завершает Automata из Tree, чата, терминала и overlay.
- [x] Существующие фокус, chat input и terminal shortcuts не регрессируют.
- [x] Проходят gofmt, go vet, полный go test, сборка и cuetty E2E.

## Результаты проверки

- Unit: root Help/Settings overlay, callback Tree и Ctrl+C из всех focus areas — успешно.
- E2E: Help/Settings находятся за пределами Tree-панели, выбор темы сохраняется, Ctrl+C закрывает Automata из non-tree focus — успешно.
- Полные `go vet ./...`, `go test ./... -count=1 -p 1` и `go build -o automata .` — успешно.
- cuetty smoke свежего бинарника: Help, Settings и Ctrl+C — успешно.
