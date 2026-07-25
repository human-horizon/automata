# Устранение deadlock OSC 7 в Portalis

## Контекст

После выбора нового чата или терминала дочерний процесс запускается, но панель перестаёт обновляться и клавиатурный ввод не доходит до shell/Pi.

Точная причина подтверждена raw PTY trace и кодом Portalis:

1. Shell отправляет первый chunk с `OSC 7`, сообщающим текущий рабочий каталог.
2. `Emulator.Update(PtyOutputMsg)` захватывает `Emulator.mu` и вызывает `parser.Feed`.
3. Parser обрабатывает `OSC 7` и синхронно вызывает CWD callback.
4. CWD callback повторно вызывает `Emulator.mu.Lock()`.
5. `sync.Mutex` не реентерабелен, поэтому обработка первого chunk зависает навсегда.

Raw trace содержит следующие chunks:

- `OSC 7` и управляющие последовательности;
- prompt `Pro:automata a$`.

При этом экран остаётся пустым либо показывает только текст до `OSC 7`, а последующие chunks и клавиши не обрабатываются.

Промежуточная гипотеза о неправильном focus/listener оказалась неверной; соответствующие изменения Automata полностью отменены и unit-тесты после отката проходят.

## Цель

Устранить повторный захват mutex при обработке `OSC 7`, сохранив потокобезопасное обновление CWD. После исправления новые и восстановленные сессии должны непрерывно обрабатывать PTY-вывод и клавиатуру.

## Что изменится

1. `portalis/emulator.go` — единая инициализация parser/CWD callback без повторного захвата `Emulator.mu`.
2. `portalis/emulator_test.go` — regression-тест, отправляющий `OSC 7` через `Emulator.Update` и проверяющий отсутствие блокировки, обновление CWD и обработку текста после OSC.
3. `portalis/CONTEXT.md` — запись о deadlock и обязательном lock-контракте callback.
4. `automata/CONTEXT.md` — запись о пользовательском проявлении и зависимости от исправления Portalis.
5. Существующие `automata/e2e/terminal_hang_test.go` и `automata/e2e/session_id_test.go` — без изменения сценариев; они подтверждают исправление в реальном TUI.

## Детали реализации

1. Создать в `Emulator` единый helper инициализации parser, используемый `StartSync` и `StartWithEnv`.
2. CWD callback должен считать, что вызывается из `parser.Feed` при уже удерживаемом `Emulator.mu`:
   - не вызывать `Lock`/`Unlock` повторно;
   - обновить `e.cwd` только при изменении пути;
   - вызвать `OnCWDChange`, если callback задан.
3. Сохранить внешний lock вокруг `parser.Feed`, чтобы `View`, resize и keyboard handling не видели частично обновлённый экран.
4. Regression-тест выполнять с таймаутом и без запуска внешнего процесса:
   - настроить parser тем же production helper;
   - передать chunk `OSC 7 + prompt` через `Emulator.Update`;
   - проверить, что Update завершился;
   - проверить обновлённый CWD;
   - проверить наличие prompt на Screen.
5. Не менять Automata lifecycle, focus или listener routing: они не являются причиной deadlock.
6. После Portalis-проверок пересобрать Automata, использующую локальный `replace` на Portalis, и повторить проблемные E2E.

## Критерии приёмки

- [ ] `Emulator.Update` с `OSC 7` завершается без deadlock.
- [ ] CWD обновляется и `OnCWDChange` вызывается ровно при изменении пути.
- [ ] Текст после `OSC 7` обрабатывается в том же PTY chunk.
- [ ] Listener продолжает принимать следующие chunks.
- [ ] Новый root chat показывает prompt и принимает ввод.
- [ ] Новый folder chat показывает prompt и принимает ввод.
- [ ] Новый terminal показывает prompt и выполняет `echo ok`.
- [ ] Восстановление CWD terminal проходит E2E.
- [ ] Все тесты и `go vet` Portalis проходят.
- [ ] Все тесты и `go vet` Automata проходят.
- [ ] Свежий `automata` успешно собран.
