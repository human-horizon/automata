# Исправление Clear: замена emulator в активной ChatPanel

## Контекст

При нажатии `Clear` в Automata выполняется три шага:

1. Останавливается старый `*portalis.Emulator` и удаляется из `emulatorCache`.
2. Удаляется файл истории `.jsonl`.
3. Создаётся новый `*portalis.Emulator`, кладётся в `emulatorCache[sessionID]`, запускается синхронно через `StartSync`, возвращается `PtyReadyMsg`.

PTY реально перезапускается, и `routeCachedEmulatorMessage` направляет PTY-выход в новый эмулятор через `emulatorCache`. Однако визуальный слой `ChatPanel` хранит ссылку на старый, уже остановленный эмулятор — поле `chatSession.em` и оборачивающий `TermPanel.em` указывают на тот объект, который был остановлен, поэтому `View(width, height)` рисует пустой экран.

Существующая спецификация `clear-session-pi-agent-dir.md` покрывает только передачу `PI_CODING_AGENT_DIR` и сигнала перезапуска. Она не описывала обновление UI после Clear, поэтому регрессионного теста на видимый результат нет.

## Цель

После `Clear` пользователь видит чистый терминал с новым процессом pi, а не пустой экран над мёртвым эмулятором. Сам процесс Clear работает идентично, добавляется только обновление ссылки на эмулятор в активной панели чата.

## Что изменится

1. `internal/ui/term_panel.go` — добавить `func (p *TermPanel) SetEm(em *portalis.Emulator)`. Метод атомарно заменяет поле `em`, никаких побочных эффектов.
2. `internal/ui/chat_panel.go` — добавить `func (s *chatSession) SetEm(em *portalis.Emulator)`, который заменяет и `s.em`, и `s.panel.SetEm(em)`. Это единая точка замены для обоих полей.
3. `main.go::clearSessionCmd` — после успешного `StartSync` для `newEm` найти активный `ChatPanel`, отыскать в нём `chatSession` с совпадающим `sessionID` и вызвать `s.SetEm(newEm)`. Если активная панель — не `ChatPanel`, ничего не делать.
4. `main_view_test.go` — добавить регрессионный тест `TestClearReplacesPanelEmulator`, который:
   - создаёт `App` с минимальной структурой,
   - подсовывает `ChatPanel` с одним `chatSession` через `container.SetChat`,
   - запоминает ссылку на исходный `*portalis.Emulator`,
   - вызывает `clearSessionCmd`,
   - проверяет, что `emulatorCache[sessionID]` и `cp.sessions[0].em` указывают на один и тот же новый объект,
   - проверяет, что `cp.sessions[0].panel.em` — это тот же объект (через `View` после `Portalis.ResizeMsg`).
5. `CONTEXT.md` — записать проблему и решение отдельной записью.

## Детали реализации

1. `TermPanel.SetEm` — простая замена `p.em = em`. Не вызывает `Stop` или `Start` — этим занимается вызывающий (`clearSessionCmd`), который уже погасил старый эмулятор перед созданием нового.
2. `chatSession.SetEm` — зеркальная замена: `s.em = em; s.panel.SetEm(em)`. Если `s.panel == nil`, защищаемся `if s.panel != nil`.
3. Поиск активной ChatPanel в `clearSessionCmd`:
   ```go
   if panel := a.container.Active(); panel != nil {
       if cp, ok := panel.(*ui.ChatPanel); ok {
           for _, s := range cp.Sessions() {
               if s.Em != nil && s.Em.SessionID == sessionID {
                   s.SetEm(newEm)
                   break
               }
           }
       }
   }
   ```
   Для доступа к полям добавить экспортёры в `chat_panel.go`: `func (cp *ChatPanel) Sessions() []*chatSession` и `func (s *chatSession) Em() *portalis.Emulator`. Без них type-assertion через interface невозможен.
4. Регрессионный тест использует `startEmulatorSyncFn` инъекцию (как уже сделано в `TestClearRestartsChatWithConfiguredPiAgentDir`), чтобы не запускать реальный PTY. Проверка UI делается через равенство ссылок и проверку `View` после `ResizeMsg`.
5. Чтобы тест был детерминирован, после `clearSessionCmd` шлём `warp.ResizeMsg{Width: 80, Height: 24}` через `cp.Update`, синхронизируем геометрию эмулятора и сравниваем `View()` с `emptyEmulatorView` — заведомо пустой строкой из самого эмулятора.

## Критерии приёмки

- [ ] После `Clear` ссылка `cp.sessions[i].em` совпадает с `a.emulatorCache[sessionID]`, а `cp.sessions[i].panel.em` — это тот же объект.
- [ ] `View()` панели после `Clear`+`ResizeMsg` отдаёт строку, сгенерированную **новым** эмулятором, а не пустоту старого.
- [ ] Регрессионный тест `TestClearReplacesPanelEmulator` падает до фикса (показывает старый stopped em) и проходит после.
- [ ] Существующий `TestClearRestartsChatWithConfiguredPiAgentDir` продолжает проходить без изменений.
- [ ] `go vet ./...` проходит.
- [ ] `go test ./internal/...` проходит.
- [ ] `go build -o automata .` проходит.
- [ ] Полный ручной сценарий: открыть чат → набрать сообщение → Clear → пустой экран перерисовывается свежим `pi` prompt (а не остаётся мёртвым).
