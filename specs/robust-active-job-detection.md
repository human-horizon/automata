# Устойчивое обнаружение активных jobs

## Контекст

Правая панель Automata всё ещё может показывать пустой список, когда у чата есть живые фоновые процессы. Текущая логика `pidIsSameProcess` в `internal/ai-knowledge/jobs/reader.go`:
- возвращает `false`, если `ps -p` завершился с ошибкой;
- требует совпадения `startedAt` с `ps -o lstart` в окне ±2 секунды, иначе трактует процесс как «не тот» (recycled PID).

На macOS `ps` иногда возвращает exit 1, если процесс исчез между `kill(0, …)` и `ps -p`, и `startedAt` записан в UTC, а `ps` отдаёт локальное время. В результате живая job, стартовавшая ≤ 2 секунды назад, или job, чей `ps` упал, помечаются как «мёртвые» и исчезают с панели.

## Цель

Сделать обнаружение живой job устойчивым: процесс, который действительно жив, всегда остаётся в списке, мёртвый или переиспользованный PID никогда не считается «тем же».

## Что изменится

1. `internal/ai-knowledge/jobs/reader.go` — переписать `pidIsSameProcess`:
   - `syscall.Kill(pid, 0)` — единственный источник истины; если возвращает ошибку, PID мёртв;
   - `startedAt` обязателен только если `ps` доступен; ошибка `ps` → доверяем `kill(0)`;
   - окно сравнения `startedAt` с `ps` расширить до 5 секунд и нормализовать timezone через `time.Local`;
   - на macOS допускать пустое `ps`-время, если процесс жив.
2. `internal/ai-knowledge/jobs/reader.go` — `List` не должен тихо перезаписывать `job.json` статусом `exited`, если сигнатура не совпала. Вместо этого: `startedAt` оставлен, а удаление/пометка делается только при явном `job_stop` или `job_list` через отдельную функцию `PruneStaleSession(sessionID)`, которая отмечает `exited` и удаляет соответствующую запись.
3. `internal/ai-knowledge/jobs/reader.go` — добавить `JobsCount` или аналог, чтобы правая панель могла отдельно отслеживать «есть ли вообще running-кандидаты», прежде чем падать в рендер.
4. `internal/ai-knowledge/jobs/reader_test.go` — расширить регрессии:
   - живой PID с `startedAt`, отличающимся от `ps lstart` на 1-3 секунды;
   - мёртвый PID;
   - reused PID (живой PID, чьё `ps` показывает другое время);
   - `ps` отказал (смоделировать через временный PATH), `kill(0)` жив.
5. `internal/ui/knowledge_panel.go` — добавить команду `pruneJobsCmd()`, которая раз в N сообщений или при смене сессии вызывает `PruneStaleSession`. Удалять job.json со статусом running строго запрещено.
6. Тесты: `knowledge_panel_test.go` — добавить `TestKnowledgePanelJobsChangeMsgReloads` и `TestKnowledgePanelPrunesStaleJob`.
7. `CONTEXT.md` — запись проблемы/решения.

## Детали реализации

1. В `pidIsSameProcess`:
   - `syscall.Kill(pid, 0)` вернул `nil` → процесс жив. Если `startedAt == ""` или `time.Parse` провалился, вернуть `true` (доверяем kill).
   - Запускаем `ps -p pid -o lstart=`. Если `exec.Command` вернул ошибку (нет `ps` в PATH) или вывод пустой — вернуть `true`.
   - Парсим `ps` в `time.Local`, приводим к UTC, сравниваем `|jobTime - psTime|`. Окно 5 секунд (clock skew на macOS в kqueue).
2. `List` теперь никогда не мутирует `job.json` со статусом running, если `kill(0)` не подтвердил смерть.
3. `PruneStaleSession(sessionID)`:
   - для каждой записи со статусом running: если `pidIsSameProcess == false`, пометить `exited` и удалить каталог (по аналогии с текущим `List`).
4. В `KnowledgePanel.Update`:
   - `jobsChangedMsg` → вызвать `PruneStaleSession`, затем `k.jobsReader.List`.
5. `JobsCount(sessionID) (int, error)` — подсчёт `running` записей без чтения содержимого `job.json`.

## Критерии приёмки

- [x] Живая job, чей `ps` отказал или вернул пустой lstart, остаётся в списке, пока `kill(0)` жив.
- [x] Живая job с расхождением `startedAt` ≤ 5 секунд остаётся в списке.
- [x] Мёртвый PID помечается `exited` и удаляется при следующем `PruneStaleSession`.
- [x] Reused PID с другим `lstart` (> 5 секунд) помечается `exited`.
- [x] `List` не пишет `exited` на диск сам по себе.
- [x] `gofmt`, `go vet ./...`, `go test ./...` зелёные.
- [x] Бинарник Automata пересобран.
