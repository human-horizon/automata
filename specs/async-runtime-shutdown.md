# Асинхронная остановка runtime jobs в UI

## Цель

Не блокировать Bubble Tea update loop ожиданием SIGTERM/SIGKILL и подтверждением завершения Pi jobs. Сохранить preflight process identity и fail-closed семантику.

## Область

Перевести ожидание job shutdown из UI callbacks в `tea.Cmd` для:

- кнопки Stop у session;
- post-commit cleanup при удалении item;
- post-commit cleanup после rename/move session;
- закрытия familiar;
- cleanup после внешнего удаления familiar.

Операцию Clear оставить асинхронной и включить в общий контракт, чтобы она резервировала владельца и familiar-сессии от параллельных shutdown. Перенести её изменения runtime/UI state в typed completion handlers; подготовка/остановка jobs и удаление данных остаются в `tea.Cmd`. Shutdown приложения остаётся bounded и синхронным вне Bubble Tea Update loop.

## Требования

1. `Update` и Tree/ChatPanel callbacks только валидируют запрос, отмечают pending operation и создают command; они не ждут завершения процесса.
2. До первого сигнала собрать и проверить полный набор планов. Любая ошибка подготовки отменяет destructive shutdown.
3. `tea.Cmd` работает с неизменяемым snapshot целей и не мутирует `App`, `Tree`, `ChatPanel` или их maps. Результат возвращается типизированным completion message. Для Clear остановка jobs и filesystem cleanup выполняются в первой команде; runtime/UI state меняется в обработчике результата, запуск Pi — во второй команде с отдельным completion.
4. UI state и кэши меняются только при обработке completion message в `App.Update`.
5. Явные Stop/Close при ошибке shutdown сохраняют runtime state и пользовательские данные. Clear при ошибке preflight сохраняет runtime; при ошибке фактической остановки применяет committed inactive state, но не удаляет истории/registry и не перезапускает Pi. Post-commit cleanup Delete/Rename/Move остаётся committed и сообщает ошибку cleanup без ложного отката tree state.
6. Повторный запрос для той же сессии во время pending операции не запускает второй kill plan. Clear держит резерв owner/familiar IDs до ошибки или completion перезапуска; иерархическая проверка резерва также блокирует запуск нового familiar-потомка во время операции владельца. Завершение устаревшей операции не должно менять runtime state другой сессии.
7. Завершение приложения вне Bubble Tea Update loop может оставаться синхронным и bounded.

## Проверки приёмки

- [x] Regression вызывает `App.Update` с реальным Tree Stop-click и блокирующим fake process stop; проверяет, что `Update` возвращается, затем completion сохраняет/изменяет runtime только на UI thread.
- [x] Отдельные тесты проверяют success/error completion, повторный запрос, Clear reservation от начала jobs shutdown до restart completion, сохранение данных при fail-closed ошибке и warning после post-commit cleanup failure.
- [x] Поиск по всем вызовам `ExecuteKillPlansForProfile`/`executePreparedSessionJobs` подтверждает: UI shutdown waits выполняются в `tea.Cmd`; оставшиеся синхронные helpers используются тестами, либо обслуживают shutdown вне Update.
- [x] `Update`/callbacks создают typed asynchronous operations; completions меняют UI/runtime state.
- [x] Явный familiar Close и Stop сохраняют runtime state при ошибке остановки; post-commit Delete/Rename/Move показывают cleanup warning без rollback.
- [x] Clear резервирует owner/familiar и descendant IDs до restart completion; preflight/job-stop failure не запускает Pi и не удаляет данные/registry. Emulator создаётся в `App.Update`, вторая `tea.Cmd` только запускает его и возвращает typed completion.
- [x] Sync shutdown helpers проверены поиском: production UI ждёт jobs только внутри `tea.Cmd`; оставшиеся sync paths — test helpers и завершение процесса вне Update.
- [x] Финальный validation: `go mod verify`, gofmt, vet, golangci-lint (0 issues), govulncheck (0 reachable; 1 module-only advisory), host/Linux amd64/macOS arm64 builds, полный `go test ./... -count=1 -p 1`, полный `go test -race ./... -count=1 -p 1`, две воспроизводимые сборки SHA-256 `30ce37d04bca2b3552e9017acd5fe144904558658559908bc764c25bb8e69480` и `git diff --check` прошли.

Изолированный self-building `TestRootChatSessionID` остаётся timing-sensitive: в финальном gate был prompt-timeout, немедленный отдельный повтор прошёл; полный E2E suite и race suite также прошли. Учитывать как известную нестабильность, а не подтверждённый устойчивый регресс.
