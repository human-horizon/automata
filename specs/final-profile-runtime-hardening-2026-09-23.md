# Финальная профильная и runtime-коррекция PR #2

## Контекст

На HEAD `41750d911e17e72695a945e3a67d06bf9834d881` остаются три границы: Pi child наследует конфликтующий `AI_PROFILE`; transactional rollback `Tree.SetActiveSessions` после уже committed runtime change временно оставляет UI с ложным состоянием; prefix `profile__` нельзя без проверки filesystem однозначно отличить от знакомого `owner__familiar`.

`specs/profile-support.md` также противоречит каноническому контракту, по которому явный `profile == ""` означает `profiles/default`, а не значение окружения.

## Цель

Закрыть три runtime/profile regressions и согласовать профильную документацию, сохранив lifecycle, explicit-profile и migration invariants PR #2. Изменения остаются в существующей ветке и PR #2.

## Что изменится

1. `main.go`, `main_view_test.go` — canonical `AI_PROFILE` и `AUTOMATA_PROFILE` для каждого Pi child; общий helper для старта назначенной задаче сессии и сохранения runtime truth.
2. `main_lifecycle.go`, `main_lifecycle_test.go` — восстановление Tree active-session snapshot после persistence error только после committed runtime stop.
3. `internal/paths/legacy_profile.go`, `internal/paths/legacy_profile_test.go` — общий filesystem-aware resolver с режимами read-only и destructive.
4. `internal/ai-knowledge/context/reader.go`, tests — resolver для legacy read/cached-read convenience APIs.
5. `internal/ai-knowledge/jobs/reader.go`, tests — resolver для legacy read APIs; ambiguity rejection до `KillSession` и `PruneStaleSession` side effects.
6. `specs/profile-support.md`, эта спецификация и `CONTEXT.md` — canonical child env, разделение explicit/legacy contracts и результаты.

## Детали реализации

### Pi child environment

`piLaunch` один раз вычисляет `profile := paths.ProfileSlug(a.profile)` и добавляет в `env` ровно по одной переменной `AI_PROFILE=<profile>` и `AUTOMATA_PROFILE=<profile>`. Это действует с обычным Pi executable и при `PI_CMD`; `PI_CMD` меняет только executable/arguments.

### Runtime truth при persistence failure

Добавить `App.persistRuntimeActiveSessions() error`: вызвать `tree.SetActiveSessions(a.activeSessions)`; при ошибке сразу восстановить только in-memory Tree через `SetActiveSessionsInMemory(a.activeSessions)` и вернуть исходную ошибку.

Использовать helper после committed runtime mutations в трёх местах: `stopSessionRuntime`, обработчик `PtyReadyMsg` и успешный `StartSync` пути назначения Kanban-задачи. Caller сохраняет наблюдаемость ошибки (возврат для Stop, логирование для callback/PTY event). Не применять к rollback/pre-commit rename/move и к transactional `restoreSessions` reset.

### Legacy profile resolution

Общий resolver строит дедуплицированный порядок кандидатов: slug до первого `__`, canonical slug текущего `AI_PROFILE`, затем `default`. Пустой session ID не проверяет корни профилей как будто они session directories.

Для каждого кандидата проверяется существование `paths.SessionDir(candidate, sessionID)`. Ошибки filesystem, кроме отсутствующего пути, возвращаются caller-у.

- При одном существующем candidate используется он.
- При нескольких read-only APIs выбирают первый существующий candidate в документированном legacy-порядке `prefix → AI_PROFILE → default`.
- При нескольких destructive candidates `KillSession` и `PruneStaleSession` возвращают `paths.ErrAmbiguousLegacySessionProfile` до чтения/изменения job records и до сигналов.
- При отсутствии существующих каталогов read-only и destructive wrappers сохраняют fallback `prefix → AI_PROFILE → default`.
- `ReadForProfile`, `ListForProfile`, `RunningCountForProfile`, `KillSessionForProfile`, `PruneStaleSessionForProfile` и прочие explicit-profile пути остаются независимыми от env.

### Документация

`specs/profile-support.md` должна говорить: для explicit Automata API пустой profile всегда canonical `default`; `AI_PROFILE` — только compatibility fallback legacy APIs. Pi child получает обе canonical переменные. Отдельно описать, что `__` в familiar ID не доказывает profile prefix и destructive ambiguity отклоняется.

## Критерии приёмки

- [x] Pi `piLaunch` задаёт canonical `AI_PROFILE` и `AUTOMATA_PROFILE` ровно по одному разу для default/named profile и `PI_CMD`, перекрывая конфликтующие значения окружения.
- [x] После Stop commit + `SaveState` failure App и Tree оба показывают inactive; возвращённая ошибка сохраняет persistence cause, emulator/running state очищены.
- [x] После `PtyReadyMsg` + `SaveState` failure App, `runningSessions` и Tree показывают active; persistence failure логируется/возвращается helper-ом.
- [x] После успешного `StartSync` назначения задачи + `SaveState` failure App, cache/running state и Tree показывают active.
- [x] Legacy read разрешает существующую default-сессию `chat__expert` в `default`, а не в несуществующую/decoy `chat`; named-prefix, AI_PROFILE и default fallback покрыты.
- [x] `KillSession("foo__bar")` при обоих каталогах `default` и `foo` возвращает ambiguity error и посылает ноль сигналов; `PruneStaleSession` при той же неоднозначности не меняет job records.
- [x] Все explicit-profile APIs сохраняют текущий isolation contract, предыдущие PR #2 regressions остаются зелёными.
- [x] `specs/profile-support.md` описывает актуальные explicit, child-env и legacy resolver contracts.
- [x] Targeted tests, `gofmt -l .`, `go vet ./...`, `go mod verify`, полный `go test ./... -count=1 -p 1`, `git diff --check` и два идентичных reproducible build проходят.
- [ ] Корректирующий commit отправлен в существующий PR #2; local/remote SHA совпадают, worktree чистый; push и pull_request CI этого SHA зелёные; PR не смёржен.

## Результаты локальной проверки

- Targeted suites: `internal/paths`, `internal/ai-knowledge/context`, `internal/ai-knowledge/jobs`, `internal/tree`, `internal/ui` и root profile/runtime regressions прошли.
- `gofmt -l .`, `go vet ./...`, `go mod verify` и `git diff --check` прошли.
- Полный CI-подобный `go test ./... -count=1 -p 1` с временным свежим `AUTOMATA_BIN` прошёл; tracked executable не перезаписывался.
- Две сборки `go build -trimpath -buildvcs=false` идентичны: SHA-256 `f2b0b2f227b93a5108855c67da2be3f9ec923845f16702f2bc057aeadd9a2b22`.
