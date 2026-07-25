# Вертикальный скролл колонок канбана

## Контекст
Сейчас в `kanbanColPanel.View()` задачи рендерятся сверху вниз, и если они не помещаются по высоте колонки — просто обрезаются (`break`). Пользователь видит только первые N задач, остальные недоступны.

Нужен вертикальный скролл внутри каждой колонки: клавиши ↑↓ для перемещения между колонками, скролл колёсиком мыши, отображение индикатора «есть ещё задачи».

## Цель
Добавить вертикальный скролл внутри каждой колонки канбана: scrollOffset по строкам, навигация ↑↓/колесо мыши, индикатор скролла.

## Что изменится
1. `internal/ui/kanban_panel.go` — `kanbanColPanel`: добавить `scrollOffset`, обработку клавиш ↑↓, колеса мыши, изменить `View()` для отображения с учётом скролла.

## Детали реализации

### 1. Поля kanbanColPanel
Добавить:
- `scrollOffset int` — смещение в строках от начала колонки (0 = первая строка видна)

### 2. View() — учитывать scrollOffset
Сейчас:
```go
for i, task := range c.tasks {
    // ...
    if linesUsed+cardHeight > height {
        break
    }
    // ...
}
```

Новая логика:
- Пропускать строки до `scrollOffset` (не рендерить, но считать linesUsed)
- Рендерить только те строки, которые попадают в видимую область
- Если после рендера остались невидимые строки — добавить индикатор `▼ ещё N`

```go
func (c *kanbanColPanel) View(width, height int) string {
    c.width = width
    c.height = height
    col := kanbanColumns[c.colIndex]

    var b strings.Builder

    // Header
    header := fmt.Sprintf(" %s (%d) ", col.label, len(c.tasks))
    header = padOrTruncate(header, width)
    b.WriteString(col.style.Render(header))
    b.WriteString("\n")

    // Предрасчёт высоты каждой карточки
    cardHeights := make([]int, len(c.tasks))
    for i, task := range c.tasks {
        cardHeights[i] = cardTotalLines(task) + 1 // +1 за gap
    }

    // Считаем, сколько строк "съедает" scrollOffset
    skippedLines := 0
    firstVisible := -1
    for i, h := range cardHeights {
        if skippedLines+1 >= c.scrollOffset { // +1 за header
            firstVisible = i
            break
        }
        skippedLines += h
    }
    if firstVisible < 0 {
        firstVisible = len(c.tasks)
    }

    // Рендерим с firstVisible
    linesUsed := 1 // header
    renderedAny := false
    for i := firstVisible; i < len(c.tasks); i++ {
        task := c.tasks[i]
        isHover := i == c.hoverRow
        cardLines := c.renderCard(task, isHover)
        cardHeight := len(cardLines)

        if linesUsed+cardHeight > height {
            break
        }
        for _, line := range cardLines {
            b.WriteString(line)
            b.WriteString("\n")
        }
        linesUsed += cardHeight
        renderedAny = true

        // Gap
        if linesUsed < height && i < len(c.tasks)-1 {
            b.WriteString(strings.Repeat(" ", width))
            b.WriteString("\n")
            linesUsed++
        }
    }

    // Индикатор "есть ещё задачи"
    if firstVisible+1 < len(c.tasks) || (firstVisible == 0 && len(c.tasks) > 0 && linesUsed >= height) {
        // Есть ещё невидимые задачи
        remaining := len(c.tasks) - (firstVisible + 1)
        if remaining < 0 {
            remaining = 0
        }
        // Если мы не дошли до конца из-за высоты — показываем индикатор
        if linesUsed < height {
            indicator := fmt.Sprintf(" ▼ ещё %d", remaining)
            if lipgloss.Width(indicator) > width {
                indicator = indicator[:width]
            }
            b.WriteString(padOrTruncate(indicator, width))
            b.WriteString("\n")
            linesUsed++
        }
    }

    // Fill remaining
    for i := linesUsed; i < height; i++ {
        b.WriteString("\n")
    }
    return b.String()
}
```

### 3. Обработка клавиш ↑↓ ← → в KanbanPanel.Update()
Когда фокус на канбане (нет pendingTask):
- `up` / `down` — скролл активной колонки
- `left` / `right` — переключение активной колонки
- Нужно понятие "активной колонки" — `activeCol int`

Добавить в `KanbanPanel`:
- `activeCol int` — индекс активной колонки (0 = Todo)

В `Update()`:
```go
case tea.KeyMsg:
    switch msg.String() {
    case "up":
        if k.activeCol >= 0 && k.activeCol < len(k.colPanels) {
            cp := k.colPanels[k.activeCol]
            if cp.scrollOffset > 0 {
                cp.scrollOffset--
            }
        }
    case "down":
        if k.activeCol >= 0 && k.activeCol < len(k.colPanels) {
            cp := k.colPanels[k.activeCol]
            maxOffset := cp.maxScrollOffset()
            if cp.scrollOffset < maxOffset {
                cp.scrollOffset++
            }
        }
    case "left":
        if k.activeCol > 0 {
            k.activeCol--
        }
    case "right":
        if k.activeCol < len(kanbanColumns)-1 {
            k.activeCol++
        }
    case "esc":
        k.pendingTask = nil
    }
```

### 4. maxScrollOffset() для kanbanColPanel
```go
func (c *kanbanColPanel) maxScrollOffset() int {
    totalLines := 1 // header
    for _, task := range c.tasks {
        totalLines += cardTotalLines(task) + 1 // +1 gap
    }
    if totalLines <= c.height {
        return 0
    }
    return totalLines - c.height
}
```

### 5. Обработка колеса мыши
В `handleMouse()`:
```go
case tea.MouseButtonWheelUp:
    if colIdx >= 0 && colIdx < len(k.colPanels) {
        cp := k.colPanels[colIdx]
        if cp.scrollOffset > 0 {
            cp.scrollOffset--
        }
    }
case tea.MouseButtonWheelDown:
    if colIdx >= 0 && colIdx < len(k.colPanels) {
        cp := k.colPanels[colIdx]
        maxOff := cp.maxScrollOffset()
        if cp.scrollOffset < maxOff {
            cp.scrollOffset++
        }
    }
```

### 6. Визуальная индикация активной колонки
Активная колонка (та, которая реагирует на ↑↓) — подсветить заголовок или добавить `▎` слева.

### 7. Сброс scrollOffset
При `SetDomain()` и `reload()` сбрасывать `scrollOffset` у всех колонок.

## Критерии приёмки
- [ ] Если все задачи помещаются по высоте — скролл неактивен, поведение как сейчас
- [ ] Если задач больше, чем помещается — ↑↓ скроллят активную колонку
- [ ] Колесо мыши скроллит колонку под курсором
- [ ] ← → переключают активную колонку
- [ ] Клик по колонке делает её активной
- [ ] Индикатор `▼ ещё N` показывается, если есть невидимые задачи
- [ ] Скролл не уходит за границы (нельзя уйти выше 0 или ниже последней задачи)
- [ ] При смене домена скролл всех колонок сбрасывается
- [ ] Все существующие тесты проходят
