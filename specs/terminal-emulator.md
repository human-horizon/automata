# Встроенный эмулятор терминала Automata

## Контекст
Automata встраивает PTY-терминал для запуска `just-pi` (или shell для тестов) прямо в правой панели. Пользователь видит полноценный TUI с ANSI-цветами, UTF-8, мышью и скроллбеком.

## Цель
Реализовать корректный рендеринг ANSI/SGR/UTF-8, поддержку ресайза и scrollback.

## Поддерживаемые возможности

### UTF-8
- Мультибайтовые последовательности собираются в буфере `utf8Buf` и декодируются через `utf8.DecodeRune`.
- Неполные последовательности заменяются на `\ufffd`.

### ANSI
- `CSI H/f` — курсор
- `CSI A/B/C/D/E/F/G` — относительное/абсолютное перемещение
- `CSI J` (0/2) — очистка от курсора / весь экран
- `CSI K` (0/1/2) — очистка строки
- `CSI s/u` — save/restore cursor (только стандартные, без private markers `? > < = !`)
- `CSI r` — scroll region (DECSTBM)
- `CSI ?1049h/l` — alternate screen buffer
- SGR: bold/dim/italic/underline/blink/reverse/hidden/strikethrough, 16/256/truecolor
- True-color SGR эмитируется десятичными значениями `38;2;r;g;b`, не hex-байтами.

### Ресайз
- `WindowSizeMsg` форвардится в `Emulator.handleResize`.
- `Screen.Resize` сохраняет старые ячейки и расширяет/сужает сетку.
- При ресайзе сбрасывается scroll region на весь экран.
- `Pty.Resize` вызывает `pty.Setsize` и явно отправляет `SIGWINCH` дочернему процессу.

### Scrollback
- Unlimited scrollback: линии, ушедшие за верх экрана, сохраняются в `scrollback`.
- `Shift+PgUp/PgDn` и колесо мыши прокручивают view.
- Новый вывод из PTY сбрасывает view на live-экран (`ResetView`).
- `Render()` защищён от `viewOffset > len(scrollback)`.

### Wrap
- Реализован "pending wrap": после записи в последнюю колонку курсор остаётся на месте, перенос откладывается до следующего символа.
- `Clear*` и `SetCursor` сбрасывают `wrapPending`, чтобы очистка строки не вызывала случайного переноса.

### Mouse forwarding
- `Emulator.handleMouse` конвертирует bubbletea `MouseMsg` в SGR mouse sequences.
- Поддерживаются press, release, motion и wheel для левой/средней/правой кнопок.
- Последовательности пишутся в PTY через `pty.Write`, что позволяет TUI внутри эмулятора (например, ai-knowledge) получать клики.

## Критерии приёмки
- [x] Русский текст отображается корректно.
- [x] True-color SGR не оставляет висящих `38;99;84m`.
- [x] Ресайз вверх не оставляет пустоту.
- [x] Scrollback работает от начала сессии.
- [x] `Working...` в `just-pi` не дублируется.
- [x] Мышь форвардится в PTY (ai-knowledge получает клики).
