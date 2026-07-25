# Automata — контекст проекта

## Что это такое
Терминальная рабочая область AI-агента. Слева — дерево папок/чатов/терминалов, справа — активная сессия.

## Текущие модули
- `internal/tree` — виджет дерева с папками, чатами, терминалами, архивом, drag & drop, модалками.
- `internal/term` — кастомный эмулятор терминала + PTY + ANSI-парсер.
- `internal/slug` — слаг для ID сессий и префиксов профилей.
- `internal/ui` — правый контейнер с чатом, чат+план и предпросмотром папок.
- `e2e` — end-to-end тесты на основе cue-tty.
- `modules/ai-knowledge` — отдельный TUI/CLI для планов, задач, памяти (интегрируется через ContextPanel).

## Активная работа
- Исправить зависание при открытии чата.
- Обеспечить автоматический и асинхронный запуск панели планов ai-knowledge.
- Сохранять работоспособность сохранения cwd/истории терминала.
- Закрепить исправление отрисовки первой иконки в предпросмотре папок.

## Последние исправления

[2026-07-10] Проблема: PTY-сообщения неактивных Pi-чатов терялись в текущем дереве Warp, цепочка `Emulator.Listen()` прекращалась и заполненная очередь блокировала `just-pi` → Решение: `App.Update` напрямую маршрутизирует `PtyReadyMsg`, `PtyOutputMsg` и `PtyExitMsg` в cached emulator по `SessionID`, а неизвестные сессии оставляет обычной маршрутизации Warp.

[2026-07-10] Проблема: `git diff` был запущен в каталоге Automata без Git-репозитория → Решение: перед Git-командами проверять наличие репозитория; в Automata проверять целевые файлы напрямую и не считать недоступный `git diff --check` успешной проверкой.

[2026-07-10] Проблема: после исправления был выполнен `go build ./...`, который проверил компиляцию, но не обновил используемый файл `./automata`; пользователь продолжил работать на старом бинарнике → Решение: после изменения Automata обязательно выполнять `go build -o automata .`, проверять mtime executable и тестировать свежий процесс; уже запущенные экземпляры нужно перезапустить без принудительного завершения пользовательских сессий.

[2026-07-10] Проблема: первый сценарий cue-tty запустил `./automata` через daemon и завершился `write /dev/ptmx: input/output error` из-за ненадёжного относительного пути → Решение: для запуска через cue-tty daemon использовать абсолютный путь к executable и не засчитывать сценарий без итоговых артефактов и маркеров.

### 2026-06-30 — E2E-тест панели Chat+Plan проходит
- **Корневая причина**: `Container.View` вызывал `innerTab.Update(ResizeMsg)`, что запускало `ContextPanel` для старта ai-knowledge, но `View` не возвращает команды — команды `StartWithEnv`/`Listen` терялись.
- **Исправление**:
  1. Добавлен `StartSync` в Emulator — запускает PTY синхронно (без tea.Cmd).
  2. `Container.View` больше не вызывает `innerTab.Update` — только рендерит.
  3. `Container.Update` обрабатывает `ResizeMsg` и устанавливает долю разделения.
  4. `TabGroup.Update` теперь обрабатывает `ResizeMsg` (раньше проваливался в `broadcastMsg` с сырым размером warp).
  5. `Tab.Update` передаёт неизвестные сообщения (`PtyOutputMsg` и т.д.) через `broadcastMsg` дочерним элементам.
  6. `ContextPanel` запускает ai-knowledge синхронно в `SetPlanSession`.
  7. Флаг `needsListen` гарантирует однократный вызов `Listen()` после синхронного запуска.

### 2026-06-29 — Контейнер переработан на примитивы Warp
- `ContextPanel.Update` теперь транслирует `warp.ResizeMsg` в `portalis.ResizeMsg`, чтобы эмулятор ai-knowledge получал корректный размер панели до запуска PTY.
- `warp.Tab.broadcastResize` собирает команды, возвращаемые листовыми панелями; `BroadcastResize` возвращает `tea.BatchMsg`, чтобы bubbletea выполнял команды запуска боковых панелей (например, панель планов ai-knowledge).
- `Container.updateChatWithPlanMode` транслирует `ResizeMsg` контейнера в размеры, специфичные для разделения чат-терминала и панели планов.

## Известные проблемы
- Чат-сессии `just-pi` теперь по умолчанию используют `PI_SKIP_VERSION_CHECK=1`, чтобы модалка «Update Available» не блокировала TUI чата.

## Соглашения
- Сначала спецификация для новых фич (спецификации в `specs/`).
- Минимум логирования; файлы отладочных логов — no-op в production-пути.
- Использовать `go vet`, `go build`, `go test` после каждого изменения.
- Никаких коммитов от AI.

[2026-07-11] Проблема: PTY-батчинг повторно запускал Listen() из RenderTickMsg и не маршрутизировал RenderTickMsg по SessionID, из-за чего ANSI-последовательности разрывались между параллельными читателями, а фоновые чаты не обновлялись → Решение: оставить единственную listener-цепочку в PtyOutputMsg, не запускать Listen() из RenderTickMsg и маршрутизировать RenderTickMsg в кэшированный эмулятор по SessionID.
[2026-07-11] Проблема: ChatPanel.renderTabBar измерял ANSI-строку через len и обрезал bar[:width], разрывая CSI/SGR-последовательности и повреждая нижнюю часть терминала → Решение: использовать ansi.StringWidth и ansi.Truncate, проверять видимую ширину regression-тестом.
[2026-07-11] Проблема: команда gofmt была запущена из корня с путями без internal/ui → Решение: перед запуском учитывать workdir и использовать полные относительные пути от него.
[2026-07-11] Проблема: regression-тест создал []chatSession вместо фактического []*chatSession → Решение: перед созданием fixture проверять точный тип поля структуры.

[2026-07-11] Проблема: Portalis считал wide Unicode/emoji одной ячейкой, из-за чего экран чата становился шире панели и status bar повреждался → Решение: внедрена wide/combining cell-модель Portalis с continuation-ячеями; Automata пересобрана.
[2026-07-12] Проблема: отключение mode 2026 по replay без chunk/resize timeline не убрало живое размазывание, а reset/skip cursor были недопустимыми эвристиками → Решение: восстановить стандартный synchronized output, убрать RenderTick с задержкой 16 мс, сохранять ordered PTY reads по 4 КБ и лениво коммитить synchronized frame только в следующем View; cue-tty stress 400 кадров проходит за 35 мс без размазывания.
[2026-07-13] Проблема: Clear искал JSONL только в каталоге текущего cwd, поэтому не очищал AI#2, созданную в /Users/a/Space и позднее открытую в /Users/a/Space/Projects/HumanHorizon/automata → Решение: сначала искать точное совпадение id в текущем cwd, затем во всех каталогах хранилища и при нескольких совпадениях выбирать самый новый JSONL.
[2026-07-13] Проблема: `git status` повторно запущен в Automata, хотя в контексте уже записано отсутствие Git-репозитория → Решение: перед Git-командами сначала читать CONTEXT.md и проверять наличие `.git`; при его отсутствии не запускать Git вовсе.
[2026-07-13] Проблема: ширина кнопки `× Clear` вычислялась через `len`, поэтому двухбайтовый UTF-8 символ `×` считался двумя колонками и tab bar был на одну колонку короче → Решение: вычислять ширину кнопки через `ansi.StringWidth`; целевой UI-тест проходит.
[2026-07-13] Проблема: диагностический `rm -f /tmp/e2e-terminal.raw*` завершился в zsh ошибкой `no matches found`, а попытка заменить его на `find -delete` была заблокирована политикой безопасности → Решение: не удалять диагностические файлы и сразу использовать уникальный trace base с timestamp.
[2026-07-13] Проблема: новые terminal и chat-сессии в Automata не показывали prompt и не принимали клавиатуру, хотя PTY запускался и raw trace содержал prompt → Решение: устранён Portalis deadlock — Emulator.Update не повторно захватывает e.mu в CWD callback и передаёт изменения OnCWDChange после Unlock; причина локализована в Portalis, в Automata только зависимость от его исправления. Полный E2E Automata проходит.
[2026-07-15] Проблема: CPU Automata линейно рос при простое, потому что `KnowledgeRefreshMsg` каждые пять секунд и `WindowSizeMsg` создавали дополнительные вечные цепочки `blinkCmd` → Решение: оставить продолжение blink-цепочки только в обработчике `blinkMsg`; regression-тесты подтверждают отсутствие таймеров у refresh/resize, отдельный свежий экземпляр держит в среднем 0.25% CPU при простое.
[2026-07-15] Проблема: после `stream → Escape → новый stream` в чате появлялись 2–3 строки Working и дубли разделителей; причина — несколько concurrent Portalis `Listen` для одного PTY, которые нарушали порядок chunks → Решение: в Portalis введён single-flight listener guard, `AlreadyRunning` не запускает новый reader; из TermPanel удалено неограниченное full-frame debug-логирование. Portalis vet/test и Automata vet/test/build проходят; live post-fix abort-сценарий наблюдал максимум 1 Working. Старый 40-ГБ `/tmp/automata-termpanel-debug.log` удалён по разрешению Ани.
[2026-07-15] Проблема: после Clear новый pi запускался без `PI_CODING_AGENT_DIR`, а возвращённая из команды вложенная `tea.Cmd` не исполнялась, поэтому чат завершался без перезапуска → Решение: запускать новый PTY через `StartSync` с подготовленным окружением и возвращать `PtyReadyMsg`; добавлен регрессионный тест каталога агента и автоматического перезапуска.
[2026-07-15] Проблема: первый полный `go test ./... -count=1` получил таймаут модалки в `TestChatOpensWithPlanPane`, хотя остальные пакеты прошли → Решение: выполнить адресный повтор теста; повторный запуск прошёл, считать исходный сбой флапом E2E и не менять production-код без воспроизведения.

[2026-07-16] Проблема: scrollback terminal-сессий сохранял строки с исходной шириной `s.Cols`; после resize (особенно уменьшения ширины) старые строки scrollback оставались длиннее текущего `s.Cols`, и правая часть визуально обрезалась, а в `renderCells` цикл `for c := 0; c < s.Cols; c++` уже сам обрезал хвост → Решение: в `Screen.resize()` после обновления `s.Cols` вызвана `normalizeScrollback()`, которая подгоняет каждую строку `s.scrollback` до новой ширины (truncate с дропом dangling continuation, pad blank cells), плюс clamp `viewOffset` на случай если scrollback уменьшился; добавлены три регрессионных теста (`TestResizeShrinkTruncatesScrollback`, `TestResizeGrowPadsScrollback`, `TestResizeScrollbackDropsDanglingContinuation`), portalis vet/test проходит, automata vet/test/build проходит, пересобран `./automata`.

[2026-07-17] Проблема: `× Clear` очищал только главную сессию, оставляя фамильяров этой сессии жить с их JSONL и табами → Решение: в `clearSessionCmd(sessionID, cwd, familiarSIDs)` добавлена фаза 0, которая останавливает эмуляторы фамильяров, удаляет их JSONL и очищает `familiars.json` (`paths.ClearFamiliarsJSONL` пишет `[]`, чтобы `checkFamiliars` убрал табы); в `ChatPanel` добавлены экспортёры `Sessions()`, `Em()`, `FamiliarID()` и метод `FamiliarSessionIDs()`; `familiarStatePath()` теперь делегирует в `paths.FamiliarsJSONLPath`; callback `SetOnClearSession` собирает `cp.FamiliarSessionIDs()` и передаёт в `clearSessionCmd`; добавлены регрессионные тесты `TestClearKillsFamiliarsOfThisSession` и `TestClearKillsFamiliarsRespectsProfile` в `main_view_test.go`, плюс `TestFamiliarSessionIDsReturnsNonMainOnly`, `TestFamiliarSessionIDsEmptyForMainOnly`, `TestSessionsExporter` в `chat_panel_test.go`; порядок важен — стоп эмуляторов до удаления JSONL до очистки `familiars.json`, иначе живой фамильяр перепишет файл; баг `clear-session-replace-panel-em.md` (пустой экран главного таба после Clear) остаётся отдельной задачей.
[2026-07-18] Проблема: чаты из дерева запускались как «обычный pi» без `PI_CODING_AGENT_DIR`, сессии писались в дефолтный `~/.pi/agent/sessions` (доказательство: `keller__wildberries.ai.jsonl` от 18.07). Корень: три несогласованных резолвера pi-команды; `SetOnSelectChat` игнорировал `a.piAgentDir`, полагался на `just-pi` из PATH, не выставлял `AUTOMATA_PROFILE` и молча деградировал в шелл; старт через `ItemSelectedMsg → em.Start() = StartWithEnv(nil)` терял env всегда → Решение: единый `App.piLaunch(sessionID)` (PI_CMD override → /usr/local/bin/pi + PI_CODING_AGENT_DIR + AUTOMATA_PROFILE → just-pi fallback → nil без деградации в шелл); в Portalis добавлены `startEnv`/`SetStartEnv`/`StartEnv`, `Start`/`StartWithEnv(nil)`/`StartSync(nil)` используют сохранённый env, поэтому любой путь старта теперь сохраняет окружение; `createChatEmulator`/`createFamiliarEmulator`/`SetOnSelectChat`/`restoreSessions`/`SetOnTaskAssigned`/`clearSessionCmd` переведены на него; поле `pendingEmulatorEnv` удалено; `createChatEmulator` сам обрабатывает `IsTerminal` (шелл без pi-env); тесты `TestPiLaunchSetsAgentDirAndProfile`, `TestPiLaunchHonorsPiCmdOverride`, `TestCreateChatEmulatorWithoutPiCommandReturnsNil`, `TestCreateChatEmulatorTerminalUsesShellWithoutPiEnv`, обновлён `TestClearRestartsChatWithConfiguredPiAgentDir`; vet/test/e2e зелёные, бинарник пересобран. Сиротские сессии в `~/.pi/agent/sessions/` (5 шт.) НЕ перенесены — ждёт решения Ани. Спека: `specs/unify-pi-launch-env.md`.
[2026-07-23] Проблема: выделение текста в чате после прокрутки смещалось относительно видимых строк из-за несогласованных viewport- и scrollback-координат → Решение: в локальном Portalis выделение переводится в логические строки (`row - viewOffset`), а рендеринг и SelectionText используют общую систему координат; добавлены регрессионные тесты. Automata `go vet`, `go test` и `go build -o automata .` проходят.
[2026-07-23] Проблема: точечное редактирование CONTEXT.md не сработало из-за несоответствия точного текста строки → Решение: перед повторным редактированием читать актуальный фрагмент файла и копировать его без реконструкции.
[2026-07-23] Проблема: полный `go test ./...` в Automata получил единичный таймаут `TestChatOpensWithPlanPane` при открытии модалки → Решение: адресный повтор `go test ./e2e -run TestChatOpensWithPlanPane -count=1 -v` прошёл; считать это известным E2E-флапом, production-код не менять.
