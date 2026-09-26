# Automata: performance и resource hardening

**Статус:** утверждена директивой пользователя продолжить работу по неизменённой спецификации от 2026-09-25.

## Контекст

В `main` сохраняются несколько лишних фоновых ресурсов и затратных обновлений:

- `ChatPanel` перечитывает registry фамильяров каждые три секунды; watcher-сообщения Knowledge/Notes/Kanban не привязаны к поколению панели, поэтому событие старого watcher может попасть в новое состояние.
- Knowledge, Notes и Kanban продолжают держать watchers после скрытия соответствующей панели/вкладки; status использует отдельный parent watcher и отдельные watcher-ы по чатам.
- Каждое изменение CWD и истории команд немедленно синхронно сохраняет весь Tree state; App также запускает бесполезный секундный blink timer.
- Разделитель chat/Knowledge может отправлять много последовательных terminal resize во время drag.
- Порог scrollback `1000` задан вручную, reader caches неограниченно растут, два внешних launcher-процесса запускаются без `Wait`, debug log автоматически пишет в `/tmp`, CPU profile не имеет пары memprofile, а профиль вроде `Default` канонизируется в зарезервированный `default`.

До реализации проверены `main`, чистое рабочее дерево и совпадение локального/удалённого SHA `991f0d7dc598251e39f46ec969f94ac2d847f7d7`. Ветка уже синхронизирована с `origin/main`.

## Цель

Снизить фоновую нагрузку и ограничить длительно удерживаемые ресурсы, сохранив актуальность UI, управление PTY и все существующие persistence/lifecycle-инварианты. Все новые отложенные и асинхронные события должны быть coalesced, generation-bound и безопасно сбрасываться при закрытии/переключении панели.

## Что изменится

- `main.go`, `main_*_test.go` — статусные watcher-ы, incremental badges, blink, deferred metadata save, startup flags/validation, debug logging и profiling.
- `internal/ui/chat_panel.go`, `context_panel.go`, `knowledge_panel.go`, `kanban_panel.go`, `container.go`, `term_panel.go` и их тесты — familiar registry watcher, Activate/Deactivate, поколения watcher-ов и coalesced resize.
- `internal/status/status.go` и cache readers `internal/ai-knowledge/{context,jobs,memory}/reader.go` — инвалидация и ограниченные LRU-кэши.
- `internal/tree/model.go` и `internal/tree/state.go` — только необходимая интеграция metadata-save lifecycle; критические структурные commits остаются синхронными.
- `internal/ui/tmux_pane.go` — тот же scrollback budget для tmux Screen.
- Новый общий bounded-cache helper и тесты, soak/race/benchmark regression tests, `CONTEXT.md`.
- Эта спецификация; пользовательская документация CLI — в описаниях flags (`-h`), отдельный README не добавляется.

## Предлагаемые контракты реализации

### 1. Watcher lifecycle и скрытые панели

- Удалить familiar polling interval и `pollFamiliarsMsg`; отслеживать registry через fsnotify watcher на родительском каталоге, чтобы переживать atomic rename/replace файла.
- Для Chat/Familiar, Knowledge, Context/Notes и Kanban ввести явные `Activate`/`Deactivate`. `Container` активирует только отображаемую mode-панель; `ContextPanel` включает watcher только выбранной Content или Kanban вкладки. Deactivate закрывает watcher и инвалидирует поколение команд, но **не очищает** scroll offset, активную вкладку/заметку, collapse state, данные и другие UI state.
- Activate выполняет начальную проверку/refresh и создаёт watcher. Событие неактивной панели не меняет состояние. Закрытые панели остаются безопасными при уже поставленных в очередь Bubble Tea Cmd.
- Каждое watcher-сообщение включает generation и identity источника. При переключении session/profile/domain, закрытии или восстановлении watcher поколение меняется; сообщения старого поколения игнорируются. Для каждого активного watcher остаётся не более одного blocking Cmd-reader.
- Сохранить PTY процессы и Listen routing скрытых терминалов; приостанавливается слежение панели, но не пользовательский процесс и не доставка его вывода.

### 2. Единый status watcher и incremental badge

- Заменить parent/per-session `fsnotify.Watcher`-ы одним объектом `fsnotify.Watcher`, который регистрирует базовый каталог sessions и необходимые каталоги сессий, с единственным blocking Cmd-reader и общим поколением.
- События в sessions root используются для пересинхронизации набора каталогов. Внутри сессии обрабатываются только события файла с basename `status.json`; остальные файлы/подкаталоги не запускают badge refresh.
- Для события `status.json` инвалидировать одну запись status cache, перечитать только эту сессию и вызвать точечный Tree badge setter. Полный recompute оставить только для startup, изменения набора/идентичности Tree items и восстановления watcher.
- Ошибка/закрытие watcher восстанавливает generation и полный badge snapshot; stale events предыдущего watcher игнорируются.

### 3. Deferred metadata save и удаление blink timer

- Удалить App blink state, `blinkMsg` и постоянный секундный ticker.
- Только изменения `Item.CWD` и `Item.CommandHistory` coalesce-ить в один отложенный Tree state save после quiet window **250 мс**. Структурные действия, rename/move/delete, runtime active-session persistence и прочие integrity-критичные commits остаются немедленными и атомарными.
- Отложенное сообщение несёт generation; устаревшие таймеры не пишут файл. Ошибка save сохраняет dirty-состояние и видимый/logged error; очередной metadata event может повторить попытку.
- `App.Close` обязательно синхронно flush-ит оставшееся dirty metadata до освобождения Tree. Закрытие не полагается на то, что Bubble Tea обработает отложенный message.
- Regression: 100 быстрых изменений metadata дают один deferred write; flush при shutdown сохраняет последнее значение; stale timer не выполняет лишнюю запись; pre-commit persistence error не теряет dirty state.

### 4. Terminal resize при split drag

Во время drag сохранять последнее требуемое terminal-размерение и коалесцировать дорогие emulator/PTTY resize не чаще одного раза за frame/период до **33 мс**. На отпускании мыши немедленно применять точный последний размер ко всем нужным chat sessions. Изменения viewport/render dimensions, обычный window resize и Tree collapse не должны ждать drag-release и сохраняют прежнюю точность.

### 5. Scrollback budget

Добавить CLI flag `--scrollback-lines`; применять его к главным/дочерним Portalis Emulator-ам и tmux Screen. Значение по умолчанию предлагается установить в **300 строк**, флаг принимает целое неотрицательное число.

Предварительное измерение текущего Portalis (`v0.0.0-20260725152259-e20856dcea01`): 25 экранов `80×24`, на каждый подано 5000 строк шириной 80 ASCII-ячеек, после GC измерен retained heap через `runtime.MemStats`:

| Лимит строк | Retained heap для 25 экранов | На экран |
|---:|---:|---:|
| 100 | 16 820 632 байта | 672 825 байт |
| 300 | 43 907 576 байт | 1 756 303 байта |
| 500 | 70 960 360 байт | 2 838 414 байт |
| 1000 | 138 677 776 байт | 5 547 111 байт |

Это синтетический baseline экрана, а не обещание общего RSS Automata. 300 строк сохраняют втрое больше истории, чем 100, при примерно 32% heap от 1000 строк; этот компромисс выбран как default.

Утверждённая семантика: `0` означает неограниченный scrollback (как у `Screen.SetScrollbackLimit`), отрицательные значения отклоняются. У `portalis.Emulator` есть интеграционная особенность: заданный до `Start` ноль не передаётся новому Screen из-за проверки `scrollbackLimit != 0`; Automata повторно применяет limit при `PtyReadyMsg`, не меняя ownership/history и не патча модуль Portalis.

Встроенный `BenchmarkTerminalScrollbackLines` измерен на macOS Apple M1 Pro командой `go test ./internal/ui -run '^$' -bench '^BenchmarkTerminalScrollbackLines$' -benchtime=1x -benchmem`. Один Screen, 5000 строк по 80 ASCII-ячеек; значения — allocations за одну итерацию, не retained heap:

| Лимит строк | B/op | allocs/op |
|---:|---:|---:|
| 100 | 1 141 336 | 355 |
| 300 | 2 255 960 | 498 |
| 500 | 3 391 576 | 689 |
| 1000 | 6 106 856 | 1 178 |

CLI regressions проверяют default, zero и отрицательное значение; отдельные regressions проверяют main/familiar emulator и tmux Screen.

### 6. Bounded caches

Использовать общий LRU helper с bounds, без изменения возвращаемых данных/ошибок:

- status cache: максимум **1024** записей, так как запись содержит только action и filesystem signature;
- context/jobs/memory reader caches: максимум **128** session/domain записей каждый; source metadata суммарно свыше **128 KiB** не сохранять в cache (данные читаются и возвращаются как прежде).
- Invalidate удаляет запись из LRU; eviction влияет только на производительность последующего чтения. Кэши безопасны при конкурентном чтении, race tests проверяют lookup/update/invalidate/eviction.
- Stress test обходит не менее 500 разных sessions/domains и подтверждает заданные bounds.

### 7. Process/log/profile hygiene

- Запущенные через `exec.Command(...).Start()` системные launcher-ы (`open`, `zed`) должны получать ровно один асинхронный `Wait`; существующие синхронные `Run`/`Output` не менять.
- Debug file logging включается только явно через `--debug-log PATH`; без flag Automata не создаёт и не дописывает `/tmp/automata-familiar.log`. Ошибка открытия явно заданного файла показывается до запуска TUI и не маскируется.
- Добавить `--memprofile PATH`, который записывает heap profile при штатном завершении после App cleanup; существующий `--cpuprofile` сохраняется.
- Отклонять непустой `--profile`, если `paths.ProfileSlug(profile)` совпадает со slug пустого профиля (`default`), включая регистровые/slug-варианты. Пустое имя по-прежнему выбирает default.

## Сохраняемые инварианты и явные исключения

- Не менять ownership, retention, lifecycle и очистку `jobs/<id>`; не менять семантику `PruneStaleSession` и его вызовов.
- Сохранить Tree/runtime/profile/persistence/migration/familiar/Kanban integrity-контракты и post-commit семантику.
- Не перезаписывать tracked executable `automata`; все E2E builds выполнять через временный `AUTOMATA_BIN` и temp artifacts.
- Не создавать ветки, PR или дополнительные рабочие каталоги; работа и публикация только в `main`. Не коммитить/не пушить код до завершения всех проверок и готового summary.

## Критерии приёмки

- [x] Familiar polling отсутствует; переключение/закрытие поколений делает stale watcher events безвредными.
- [x] Скрытые панели закрывают watchers, при активации сразу обновляют данные, сохраняя UI state.
- [x] Status использует один watcher и один blocking reader; только `status.json` вызывает точечный badge update.
- [x] Нет глобального blink timer; 100 быстрых metadata изменений coalesce-ятся в один save и shutdown flush сохраняет финальный state.
- [x] Drag resize ограничен 33 мс coalescing-ом, release устанавливает точный размер; collapse E2E остаётся зелёным.
- [x] Scrollback benchmark и CLI regressions проходят; default `300` подтверждён полученными данными, а утверждённая zero semantics соблюдена.
- [x] Все reader caches имеют проверенные bounds; 500-session/domain soak проходит без утечки записей и stale badges.
- [x] `open`/`zed` child-процессы reaped; без `--debug-log` нет фиксированного debug-файла; `--memprofile` создаёт профиль; explicit slug `default` отклоняется.
- [x] Добавлены lifecycle churn/soak и race regressions для чатов, Context/Kanban/Knowledge, familiar churn, status watcher и cache limits.
- [x] Пройдены `go mod verify`, `gofmt -l .`, `go vet ./...`, `go test -race ./internal/... -count=1 -p 1`, полный `go test ./... -count=1 -p 1` с временным `AUTOMATA_BIN`, `git diff --check` и две совпадающие `-trimpath -buildvcs=false` сборки.
- [ ] После публикации local/remote main SHA совпадают, tracked executable не изменён, и CI успешен на том же SHA.

## Результаты локальной проверки

На 2026-09-25 прошли `go mod verify`, `gofmt -l .`, `go vet ./...`, `golangci-lint run --new-from-rev=HEAD ./...` (0 issues), `go test -race ./internal/... -count=1 -p 1`, полный `go test ./... -count=1 -p 1` с временным `AUTOMATA_BIN` и E2E artifacts, `git diff --check`. Две `-trimpath -buildvcs=false` сборки совпали SHA-256 `8c264acf3e8c2be8488d16b944d9c877d3ca9ae6b7eba65591e1f219051d9641`; tracked executable не изменён.

Дополнительный repo-wide `golangci-lint run ./...` без проектной конфигурации сообщил legacy `errcheck`/`ineffassign` diagnostics на неизменённых строках; новые diff-scoped diagnostics исправлены, повторный lint изменённого кода чистый. Commit/push/CI не выполнялись: публикация ожидает отдельного подтверждения Ани.
