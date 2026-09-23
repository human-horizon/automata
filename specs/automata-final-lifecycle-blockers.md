# Финальные lifecycle blockers Automata

## Контекст

После `be5264e` остаются три согласовательных дефекта жизненного цикла: после rename/move финализация jobs обращается к исчезнувшему старому session ID; rename переносит внешние данные до успешного `Tree.SaveState`; multi-session остановка может частично сигнализировать jobs до ошибки. Дополнительный edge case удаляет familiar tab до успешной host-очистки, а rollback move может вызвать `SaveState` на ещё новом дереве.

## Цель

Сделать rename/move и destructive runtime cleanup однозначными: job identity проверяется до сигналов, финализация использует мигрировавшие данные, rename откатывает и дерево, и внешние данные при ошибке сохранения, а UI не теряет familiar при fail-closed preflight.

## Что изменится

1. `internal/ai-knowledge/jobs/reader.go` — двухфазный immutable kill plan: prepare без сигналов, execute с bounded verification и aggregate errors.
2. `internal/ai-knowledge/jobs/reader_test.go` — preflight, partial signal и migrated-session regressions.
3. `main_lifecycle.go` — подготовка всех job plans до destructive commit, deterministic cleanup и режим восстановления runtime без промежуточного `SaveState`.
4. `main_rename.go`, `main.go` — post-commit finalization по новым IDs и transactional rename hooks.
5. `internal/tree/model.go` и тесты — rename SaveState rollback и move rollback ordering.
6. `internal/ui/chat_panel.go` и UI-тесты — host familiar cleanup до удаления tab.
7. `CONTEXT.md` — записи о выявленных проблемах и решениях.

## Детали реализации

- Для каждой целевой session сначала собрать `KillPlan`: `PIDSame` становится signal candidate, `PIDDead`/`PIDDifferent` — stale candidate, `PIDUnknown` немедленно останавливает prepare без сигналов.
- Выполнить prepare для всех owner/familiar IDs до начала destructive phase. Execute обрабатывает все candidates, подтверждает смерть bounded probe, materializes stale records и возвращает aggregate error, не прерывая cleanup на первой ошибке.
- Rename/move сохраняют post-migration mapping `oldID → newID`; после успешного `SaveState` jobs завершаются по новым IDs или сохранённым verified candidates, никогда не по исчезнувшему старому каталогу.
- Rename получает before/after transaction boundary: внешняя миграция и reversible runtime выполняются до Tree commit; при ошибке `SaveState` сначала восстанавливается старый Tree snapshot, затем выполняется внешний/runtime rollback без промежуточного сохранения нового дерева; irreversible job termination происходит только после успешного commit.
- Close familiar выполняет host cleanup до `removeSessionAt`; при ошибке tab, emulator и persisted familiar metadata остаются.

## Критерии приёмки

- [x] Реальный jobs layout после move/rename находится по migrated ID и вызывает настоящий `processSignal` через `KillSessionForProfile`.
- [x] Ошибка rename `SaveState` возвращает старое имя, старый persisted state, session/JSONL/familiars/Kanban и не отправляет job signals.
- [x] Ошибка move `SaveState` сначала восстанавливает старое дерево; rollback не сохраняет промежуточное новое дерево.
- [x] PIDUnknown в любой session multi-stop даёт ноль сигналов до commit; partial signal failure очищает все цели и возвращает aggregate error.
- [x] Failed familiar cleanup не удаляет tab, emulator или familiar metadata.
- [ ] Targeted/full tests, `gofmt`, `go vet`, `go mod verify`, reproducible builds, `git diff --check` и новый GitHub Actions run проходят.
