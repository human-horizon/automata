# Запуск и остановка Folder Actions моделью

## Контекст

Folder Actions сейчас запускаются из Automata UI после подтверждения; `KnowledgePanel` хранит отмену процесса только в памяти своего экземпляра. Pi работает в отдельном процессе, поэтому `run_action`/`stop_action` не могут безопасно управлять UI-запуском простым вызовом существующего runner.

## Цель

Добавить Pi tools `run_action({name})` и `stop_action({name})`, чтобы модель могла запускать сохранённую Action без дополнительного UI-подтверждения и останавливать её независимо от того, была ли она запущена моделью или пользователем из Automata UI.

## Что изменится

1. `just/pi/extensions/automata/index.ts` и `index.test.mjs` — регистрация/валидация Action, Pi-side запуск и остановка.
2. `internal/ai-knowledge/actions/` — общий межпроцессный run-control протокол для текущего profile/domain и action ID.
3. `internal/ui/actions_runner.go`, `knowledge_actions.go` и тесты — публикация активного запуска и реакция на stop request из Pi.
4. `specs/action-model-tools.md`, `specs/remove-folder-action.md`, `CONTEXT.md` — контракты и результаты проверок.

## Детали реализации

- Оба инструмента принимают только `name`; profile/domain берутся из активной Pi-сессии. Имя нормализуется тем же NFC/trim/lowercase SHA-256 ID, что `add_action`/`remove_action`. Запись читается и проверяется тем же fail-closed validator.
- Вызов `run_action` является явным разрешением модели: команда запускается без второго UI-подтверждения. Выполняется только сохранённая команда (`/bin/sh -c`) в сохранённом cwd; параметры tool не могут подменить command или cwd.
- UI-запуск сохраняет существующее подтверждение точной команды и cwd. После подтверждения и модельный запуск, и UI-запуск регистрируются в одном domain-scoped run-control протоколе.
- Для одной Action одновременно допускается не более одного процесса, независимо от того, кто его запускает. Гонка UI/Pi разрешается атомарным claim; проигравшая сторона получает `already_running` без запуска второй команды.
- Общий runtime-файл расположен в `actions/runtime/<actionID>.json`, содержит schema version, случайный run ID, canonical profile/domain/action ID, источник запуска, PID (после старта) и время запуска. Claim создаётся эксклюзивно; одновременная попытка второго запуска возвращает `already_running`. Чтение и запись отвергают symlink/не-regular файл, malformed record и несовпадающую provenance.
- `stop_action({name})` создаёт отдельный stop-request, адресованный текущему run ID, а не принимает PID/path от модели. Владелец процесса опрашивает request и выполняет остановку всей process group (SIGTERM, затем ограниченный SIGKILL по текущей политике); request другого/предыдущего run игнорируется. Инструмент ждёт ограниченное время подтверждения завершения и возвращает `stopped`, `not_running` или `stop_timeout`.
- UI runner и Pi runner обязаны использовать один protocol package/wire schema и единый claim. Нельзя сигналить процесс напрямую по persisted PID: только живой владелец процесса принимает stop request и сам использует уже открытый process-group handle. Это защищает от PID reuse.
- Ранее запущенные процессы без runtime-записи не затрагиваются. При естественном выходе владелец удаляет только запись/stop request собственного run ID; освобождение claim проверяет run ID. `remove_action` не останавливает текущий run и не удаляет его runtime/log files.
- Если runtime каталоги/записи повреждены или операция не может доказать владение claim, инструменты и UI fail-closed; автоматическое удаление stale runtime/PID signaling не выполняется.
- Не меняются произвольные job-команды, production confirmation, role policy и shell sandbox. В тестах runner подменён; реальные системные команды не запускаются.

## Критерии приёмки

- [x] `run_action` запускает только сохранённую Action без второго подтверждения; неизвестная запись отклоняется до spawn.
- [x] `stop_action` публикует run-bound marker для UI-run и останавливает Pi-run без PID/path input.
- [x] Go ClaimRun и Pi exclusive create отклоняют второй активный claim.
- [x] KnowledgePanel отображает shared run через подсветку, может остановить Pi-run, а постоянная строка Action-status убрана.
- [x] Run record проверяет provenance; только владелец процесса сигналит process group; UI и Pi используют bounded stop behavior.
- [x] Regressions покрывают Pi runner, duplicate claim, profile/domain isolation, UI-owned stop marker, Go owner cancellation по marker, symlink record и run cleanup.
- [x] Pi Automata targeted suite 12/12 и TypeScript typecheck проходят; полный extension suite результаты фиксируются отдельно.
- [x] Go `gofmt`, `go vet ./...`, golangci-lint (0 issues), `go mod verify`, полный `go test ./... -count=1 -p 1` (включая E2E), race для actions/UI и temporary build прошли.
- [x] Pi Automata tests 14/14; полный чистый extension suite 179/179; `pnpm exec tsc --noEmit` и scoped `git diff --check` прошли.
- [ ] PR #12 и связанный Pi PR содержат только scoped changes; оба обязательных CI green перед merge.
