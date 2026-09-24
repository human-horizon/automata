# Hardening Automata после merge PR #1

## Контекст

После merge commit `478834c1e8491ca1046944e53a1cac889874134f` в `main` остаются независимые дефекты изоляции профилей, cache invalidation, persistence error handling, resumable migration, сохранения метаданных и Unicode/UI edge cases. Главный blocker — explicit APIs с пустым профилем иногда читают `AI_PROFILE`, из-за чего canonical `profiles/default` расходится с jobs/context/memory paths и destructive operations могут работать в чужом профиле.

Уже исправленные lifecycle-инварианты не пересматриваются и не ослабляются: PID identity/KillPlan, transactional rename/move, runtime rollback, migrated job finalization, delete semantics, familiar lifecycle, watcher one-reader guards, Kanban task migration, atomic state writer, portable CI и reproducible builds.

## Цель

Сделать hardening pass на ветке от merge commit: пустой explicit profile всегда означает canonical default, caches и settings не протекают между sessions, критические persistence failures видимы, legacy migration возобновляема, metadata сохраняется, а UI/watchers безопасны для Unicode, ошибок и малых размеров.

## Что изменится

1. `internal/paths`, `internal/ai-knowledge/jobs`, `internal/ai-knowledge/context`, `internal/ai-knowledge/memory`, `internal/ui/context_panel.go` — единый explicit-profile contract без скрытого `AI_PROFILE` fallback.
2. `internal/ai-knowledge/context/reader.go` и `internal/ui/knowledge_panel.go` — полная file signature, explicit invalidation и сброс settings flags при смене session.
3. `internal/tree`, `main_lifecycle.go`, delete/stop callers и tests — ошибки критического `SaveState` не теряются, а committed destructive semantics документируются и тестируются.
4. `internal/tree/migrate.go` — resumable/idempotent legacy migration с atomic completion marker.
5. `internal/kanban/kanban.go`, `internal/paths/sessionfile.go` — сохранение неизвестных JSON/YAML metadata и atomic familiar writes.
6. `internal/ui` и `main.go` — Unicode-safe terminal truncation, watcher error recovery, bounded tiny-width layout и согласованные debug-log flags.
7. Русские regression tests, `CONTEXT.md` и эта спецификация.

## Детали реализации

### Фаза 1: profile isolation

- Explicit `*ForProfile` APIs используют `paths.ProfileDir`/`paths.SessionDir` напрямую; `profile == ""` означает `profiles/default`.
- Legacy convenience APIs сохраняют env-aware поведение только внутри legacy wrapper-а, если оно уже является контрактом.
- Добавляются tests с `AI_PROFILE=wrong-profile` и одновременными default/wrong-profile fixtures для jobs, context, memory и ContextPanel.

### Фаза 2: cache и Knowledge settings

- `context.CachedReader` сравнивает signature каждого из `plans.json`, `status.json`, `settings.json` по existence, size и mtime, включая удаление файла.
- Добавляется explicit invalidation seam и вызов на watcher event там, где panel получает `knowledgeChangedMsg`.
- `KnowledgePanel.readSettings` перед чтением сбрасывает `autoContinue` и `dual`; `SetSession("")` также возвращает defaults. Invalid/missing JSON не наследует предыдущие flags.

### Фаза 3: persistence и migration

- Lifecycle-critical callers получают `SaveState` error или явно возвращают committed-warning semantics после irreversible runtime cleanup.
- Structural Tree operations не объявляют успех при незаписанном state; tests инъецируют persistence failure для stop/delete/active cleanup.
- Legacy migration не считает наличие target directory признаком завершения. Missing files копируются повторно, destination user files не перезаписываются, completion marker пишется атомарно только после полного успешного copy/verify. Named profiles используют тот же контракт.

### Фаза 4: data preservation

- Kanban parser/writer хранит unknown frontmatter fields и меняет только известные поля. Status/substatus валидируются по действующему contract.
- `RemoveFamiliar` работает через `json.RawMessage` records и atomic temp/fsync/rename write; unknown fields оставляются. `ClearFamiliarsJSONL` также atomic.
- Memory reader различает not-exist (empty) и permission/I/O/invalid JSON (error); UI показывает diagnostic вместо silent empty, не затирая corrupted data.

### Фаза 5: UI/runtime hardening

- Terminal-width truncation использует cell/rune-aware helpers и не режет UTF-8 bytes.
- Общий watcher helper обслуживает `Events` и `Errors`, логирует error, закрывает/recreates watcher и запускает forced refresh; минимум status/session/knowledge paths покрываются injected-error tests.
- `planFraction` возвращает конечное значение в поддерживаемом диапазоне при width 1, 2, 5, 10 и других tiny widths.
- Debug log flags соответствуют выбранному persistence contract; для сохранения familiar diagnostics используется append без `O_TRUNC`.

## Критерии приёмки

- [x] Во всех explicit APIs `profile == ""` означает только canonical `default`; `AI_PROFILE` не перенаправляет destructive jobs.
- [x] Jobs/context/memory/ContextPanel default-vs-env regression tests проходят.
- [x] Context cache замечает non-max file changes, deletion, size changes и explicit invalidation.
- [x] KnowledgePanel flags не протекают между sessions и сбрасываются при missing/invalid settings.
- [x] Lifecycle-critical persistence failures имеют явный tested/documented результат; silent success устранён для выбранного scope.
- [x] Interrupted legacy migration возобновляется, idempotent и не перезаписывает destination user files.
- [x] Kanban/Familiar updates сохраняют unknown metadata и atomic writes.
- [x] Notes corruption отличима от отсутствующего файла и не выглядит как empty success.
- [x] Unicode fixtures остаются valid UTF-8 и укладываются в terminal cell width.
- [x] Watcher errors имеют recovery path и regression coverage.
- [x] Tiny-width layout bounded, debug-log contract согласован.
- [x] `gofmt -l .`, `go vet ./...`, `go test ./... -count=1 -p 1`, `go mod verify`, `git diff --check` и два reproducible builds проходят.
- [x] Новая ветка опубликована, PR в `main` открыт, merge-branch CI и clean-tree/reproducible checks зелёные.
