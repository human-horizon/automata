# Унификация запуска pi с гарантированным PI_CODING_AGENT_DIR

## Контекст

Чаты из дерева Automata иногда стартуют как «обычный pi» — без `PI_CODING_AGENT_DIR`, сессии пишутся в дефолтный `~/.pi/agent/sessions` вместо `~/.ai/just/pi/sessions`. Доказано файлами: `~/.pi/agent/sessions/--Users-a-Space--/2026-07-18T12-40-52-498Z_keller__wildberries.ai.jsonl` (создан после свежей сборки) и др.

Причина — три несогласованных резолвера команды pi в `main.go`:

1. `SetOnSelectChat` (клик по чату в дереве, ~стр. 142–156):
   - игнорирует `a.piAgentDir`, полагается на `just-pi` из PATH;
   - не выставляет `AUTOMATA_PROFILE` (только случайно залёхалый `a.pendingEmulatorEnv` от чужого `createChatEmulator`);
   - при отсутствии `just-pi` в PATH молча деградирует в дефолтный шелл с бессмысленными аргументами `--session-id`;
   - для профиля с `--pi getic` всё равно запускает just-обёртку — неправильный agent dir.
2. `createChatEmulator` (~стр. 502): `/usr/local/bin/pi` + env — корректно.
3. `createFamiliarEmulator` (~стр. 571): `/usr/local/bin/pi` + env — корректно.

`pendingEmulatorEnv` — shared-поле, записываемое в одном месте и читаемое в другом: источник гонок и залёхалых значений.

## Цель

Любой запуск pi-сессии из Automata (дерево, restore, clear-restart, familiar) стартует с `PI_CODING_AGENT_DIR=<piAgentDir>` и `AUTOMATA_PROFILE=<профиль>` — независимо от PATH и порядка действий. Никакой молчаливой деградации в шелл.

## Что изменится

1. `portalis/emulator.go` (пакет `../../Starframe/portalis`):
   - новое поле `startEnv []string` в `Emulator` и метод `SetStartEnv(env []string)`;
   - `StartWithEnv(nil)` и `StartSync(nil)` используют сохранённый `startEnv`, если env не передан явно — **это закрывает баг с `em.Start()` без env в `ItemSelectedMsg` (main.go:403, 411)**.
2. `main.go`:
   - новый метод `(a *App) piLaunch(sessionID string) (cmd string, args []string, env []string)` — единый резолвер;
   - `createChatEmulator` использует `piLaunch` и вызывает `em.SetStartEnv(env)`;
   - `SetOnSelectChat` использует `createChatEmulator` (с обработкой `item.IsTerminal`) вместо собственного резолвера;
   - `createFamiliarEmulator` использует `piLaunch`;
   - поле `pendingEmulatorEnv` удаляется;
   - отсутствие pi-команды → ошибка в лог вместо молчаливого шелла.
3. `main_view_test.go` — тесты резолвера и обновлённых путей.

## Детали реализации

### `piLaunch(sessionID string) (cmd, args, env)`

Приоритет:
1. `PI_CMD` задан в окружении → `(PI_CMD, nil, envТолькоСПрофилем)` — поведение для e2e-тестов (`PI_CMD=/bin/bash`) сохраняется.
2. `a.piAgentDir != ""` → `("/usr/local/bin/pi", ["--session-id", sessionID], [PI_CODING_AGENT_DIR=a.piAgentDir, AUTOMATA_PROFILE?])`.
3. Иначе (флаг `--pi ""` передан явно) → `just-pi` из PATH, если найден: `(путь, ["--session-id", sessionID], [AUTOMATA_PROFILE?])`.
4. Ничего не найдено → `cmd == ""`, вызывающий код пишет ошибку в лог и НЕ создаёт эмулятор (чат показывает пустую панель вместо шелла).

`AUTOMATA_PROFILE=<slug>` добавляется во env всегда, когда `a.profile != ""`.

### `createChatEmulator(sessionID) *Emulator`

- Через `piLaunch` получает cmd/args/env и вызывает `em.SetStartEnv(env)` — дальше эмулятор может стартовать любым способом (`Start`, `StartSync(nil)`) и env не потеряется.
- Терминалы (`item.IsTerminal`) обрабатываются вызывающим кодом: для терминала — `DefaultShell()`, env не нужен.

### `SetOnSelectChat`

- Для `item.IsTerminal` — прежнее поведение (шелл).
- Иначе — `createChatEmulator(sessionID)`; эмулятор кладётся в cache, старт происходит как сейчас через `ItemSelectedMsg → em.Start()`, который благодаря `startEnv` получит правильный env.
- `em == nil` → лог + return (без панели с шеллом).

### `restoreSessions`, `SetOnTaskAssigned` и `clearSessionCmd`

- Стартуют через `em.StartSync(nil)` / `em.StartWithEnv(nil)` — env берётся из `startEnv`.

### Удаления

- Поле `App.pendingEmulatorEnv` и все его упоминания/комментарии.

## Критерии приёмки

- [x] `go vet ./...` чисто
- [x] `go test ./...` зелёный (включая e2e `session_id_test.go` с `PI_CMD=/bin/bash`)
- [x] Новый unit-тест: `SetOnSelectChat`-путь создаёт эмулятор, и `Start()` без явного env использует сохранённый `startEnv` (portalis: `TestStartUsesRecordedStartEnv`, `TestStartEnvGetterRoundTrip`)
- [x] Новый unit-тест: при `--pi getic` env содержит `~/.ai/getic/pi` (`TestPiLaunchSetsAgentDirAndProfile`)
- [x] Новый unit-тест: без piAgentDir/just-pi/PI_CMD эмулятор не создаётся (нет деградации в шелл) (`TestCreateChatEmulatorWithoutPiCommandReturnsNil`)
- [x] Бинарник пересобран
- [x] CONTEXT.md дополнен записью о проблеме и решении
