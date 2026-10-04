# Финальное укрепление Automata после аудита

## Контекст

В полном handoff выявлены риски потери диагностик при откате переименования, лишнего I/O и сериализации читателей, последовательного ожидания завершения jobs, чрезмерно открытых диагностических файлов и неполной транзакционности отдельных UI-операций. Кроме того, старые каталоги сессий не имеют явной версии схемы. В присланном скриншоте Automata сообщает `decode status .../status.json: unexpected end of JSON input`: Pi extensions пишут status-файл напрямую, и читатель может попасть между truncate и завершением записи.

## Цель

Закрыть конкретные дефекты handoff и скриншота небольшими проверяемыми изменениями, сохранив текущие UX-контракты, атомарность сохранений, identity/fail-closed гарантии и совместимость старых пользовательских данных. Не менять и не удалять найденный orphan job и не ослаблять Clear preflight.

## Что изменится

1. `main_rename.go`, `main_rename*_test.go` — возвращать и объединять ошибки rollback вместо одного лишь логирования; покрыть ошибки восстановления файловой системы, JSONL, Kanban и familiar registry.
2. `internal/ai-knowledge/memory/reader.go` и тесты — проверять cache signature до декодирования файла; I/O и процессные вызовы всех CachedReader выполнять вне mutex.
3. `internal/ai-knowledge/{context,jobs}/reader.go`, `internal/status/status.go` и тесты — вынести filesystem/process I/O из критических секций, сохранив безопасную публикацию и инвалидацию cache.
4. `main_lifecycle.go`, `internal/ai-knowledge/jobs/reader.go` и тесты — подготовить единый fail-closed kill plan для всех целевых сессий, отправить SIGTERM всем проверенным кандидатам и выполнить один общий bounded polling pass.
5. `main.go` и тесты — создавать/открывать debug log и CPU/heap profiles с правами `0600`, ужесточать mode уже существующих файлов до первой записи.
6. `internal/tree/model.go`, тесты и спецификация темы — откатывать неподтверждённую смену темы при ошибке сохранения; committed durability warning не трактовать как неуспешный commit.
7. `internal/tree/model.go` и тесты — разложить `MoveItemChecked` на локальные helpers без изменения результата, порядка side effects и ошибок.
8. `internal/ui/kanban_panel.go` и тесты — выделить geometry, hit-testing/routing, status actions и card rendering в helpers без изменения клавиатурного/мышиного UX.
9. `internal/paths/sessionmeta.go` (новый), `internal/paths/paths.go`, создание Pi emulator и `syncSessionWatchers` в `main.go`, тесты — добавлять manifest `session.json` формата `{"schemaVersion":1}` при создании новых Automata-сессий. Существующий каталог без manifest читается как legacy version 0 и не переписывается автоматически; повреждённый manifest и неподдерживаемая будущая версия дают явную ошибку. Session ID, Pi JSONL, содержимое каталога и legacy migration не меняются.
10. `internal/status/status.go` и тесты — при временно неполном JSON выполнять ограниченное повторное чтение вне mutex; после исчерпания попыток сохранять явную ошибку и предыдущий кэш не подменять пустым статусом.
11. `README.md`, `CONTRIBUTING.md` (новый), `SECURITY.md` (новый), `.github/pull_request_template.md` (новый) — добавить проверяемые инструкции вклада, тестирования и приватного сообщения об уязвимостях без выдуманных контактов. Не добавлять LICENSE, CODEOWNERS или Code of Conduct без решения владельца.
12. `.github/workflows/ci.yml`, `go.mod` — менять только если проверенные Dependabot PR #4–#7 пройдут review diff, CI и release notes; иначе оставить их без изменений и записать причину.
13. GitHub settings — проверить защиту `main`; требуемая проверка — CI job `quality`. Не делать merge зависимостей и не публиковать код.
14. `CONTEXT.md` — записать каждую найденную проблему/решение в требуемом формате даты.

## Детали реализации

- Rollback собирает все ошибки с контекстом источника через `errors.Join`; исходная причина операции остаётся доступной через `errors.Is`/`errors.As`. Все шаги rollback продолжают выполняться, даже если предыдущий шаг завершился ошибкой.
- CachedReader mutex защищает только cache и короткое состояние синхронизации. `Stat`, `ReadFile`, JSON decode, directory enumeration и проверка PID выполняются вне него. Ошибка чтения не кэшируется как успешная пустая запись; invalidation во время чтения не должна восстанавливать устаревшую cache entry.
- Job shutdown сначала полностью строит общий KillPlan и отказывает до сигналов при любой неизвестной/небезопасной identity. Только после успешного preflight отправляет SIGTERM всем живым кандидатам, затем выполняет единый ограниченный по времени polling pass; повторная проверка PID identity перед signal сохраняется.
- Manifest хранится в Automata profile-scoped каталоге сессии, пишется через существующий `atomicfile` с private mode. `EnsureSessionDir` создаёт version 1 только при создании новой директории; существующая директория без manifest остаётся legacy version 0 до отдельной миграции. Чтение не изменяет данные.
- Pi writers `status.json` выполняют запись во временный файл рядом с целевым, `fsync`, закрытие и rename; временные файлы чистятся при ошибке. Это предотвращает наблюдение частичного JSON, не меняя поля статуса. Go reader сохраняет ограниченный retry для процессов со старой версией extension.
- `SetTheme` вызывает callback только для состояния, которое подтверждено сохранением; при pre-commit failure возвращает старое значение. Если сохранение зафиксировано, но сообщает ошибку durability, новое значение остаётся применённым, а предупреждение остаётся видимым.
- Move/Kanban refactors ограничены извлечением helpers; публичные API и UX не меняются.
- Не выбирать лицензию, владельцев CODEOWNERS и не запускать destructive recovery для orphan job.

## Критерии приёмки

- [x] Rollback-ошибки доступны вызывающей стороне и объединены с первоначальной причиной; тесты подтверждают попытку каждого шага.
- [x] Cache hit memory не читает/декодирует notes.json; ошибки и invalidation корректно обрабатываются при конкурентных чтениях всех четырёх readers.
- [x] Batch shutdown preflight остаётся глобальным и fail-closed; SIGTERM и ожидание выполняются общими фазами, порядок проверен детерминированным тестом.
- [x] Новые и существующие диагностические файлы имеют mode `0600` до записи.
- [x] Theme persistence failure восстанавливает старую тему, а committed warning сохраняет новую.
- [x] Move и Kanban refactors проходят прежние regressions и новые геометрические/hit-testing tests без изменения UX.
- [x] Новые сессии получают schema version 1; legacy-сессии без manifest читаются как version 0 без автоматического изменения файлов; повреждённый/будущий manifest не затирается. Проверено `go test ./internal/paths -count=1 -timeout=3m` и `go test . -count=1 -p 1 -timeout=5m`.
- [x] `status.json` больше не публикуется частичной записью; оба Pi producers обновлены, ограниченный reader retry закрывает race и persistent invalid JSON остаётся видимой диагностикой. Проверены Pi producer suites и `pnpm exec tsc --noEmit`.
- [x] Добавлены `CONTRIBUTING.md`, `SECURITY.md` и PR template без выдуманных контактов/лицензии/владельцев. Dependabot PR #6 проверен по diff и exact checks, затем squash-merged; остальные action PR проверяются отдельно.
- [x] Пройдены `go mod verify`, gofmt, vet, golangci-lint, govulncheck, host/cross/reproducible builds, полный `go test ./...`, race suite, полный и self-building E2E, `git diff --check`.
- [x] Исторический hardening baseline включал commits `216c8cd047621422aa5d6eb886f42d0896568393` и `e1198434bc28261a9e57e2ea07245d3c9552d97f`; их CI результаты сохранены выше.
- [x] Текущий проверенный baseline — squash merge commit `61e1e058f77fc31844737ffefc610bcb4623a6d5` (Dependabot #6). GitHub CI #127 успешен; branch protection требует strict `quality`, enforcement включён для admins, force-push/deletion запрещены. Локальный полный gate на этом exact HEAD прошёл: verify, format, vet, lint, govulncheck, host/cross/reproducible builds, full + self-building E2E, race, diff check.
- [ ] Live-проверка 2026-09-30: Dependabot #4/#5/#7 открыты и `BEHIND`; все меняют только `.github/workflows/ci.yml` и проверки относятся к старым base refs. #4: head `76042f8b0499f56ab7fe2c50d28846385d3354ed`, base `2dc231e7d375c482b2f5a6ca9b30ca22c047fc89`, один `quality` failure и один success. #5: head `3e78e6d4392665f795fe6470a168ce57f4603dd0`, base `7ba2b83c6744d5e68b7743b7bdeb56ba39d4da0d`, оба старых checks success. #7: head `678bbf685afc48a6a4d537d2e7645ebf2e399f0f`, тот же старый base, оба старых checks success. Не обновлять/не merge-ить без разрешённого OAuth scope `workflow`; обходить отказ нельзя.
- [ ] `.github/workflows/ci.yml` по-прежнему использует `actions/checkout@v4`, `actions/setup-go@v5`, `golangci/golangci-lint-action@v8`, не immutable SHA pins. Проверенные official release SHAs документированы в `specs/github-actions-sha-pinning.md`. Workflow не меняла: требуется отдельное разрешение владельца и доступ `workflow`.

`govulncheck -show verbose` на текущем baseline: нет уязвимостей в вызываемых символах/пакетах; module-only `GO-2026-5024` затрагивает `golang.org/x/sys@v0.38.0` Windows API (`NewNTUnicodeString`), исправлено в v0.44.0, Windows не поддерживается и affected symbol не вызывается. PR #6 обновляет ansi/display dependencies, но не x/sys; отдельный x/sys update не вносился без проверенного PR/release review.
