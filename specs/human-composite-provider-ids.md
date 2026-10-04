# Human: составные идентификаторы инструментов Pi

## Состояние

Исправление выполнено и проверено. По прямому поручению Ани продолжить все перечисленные открытые шаги установлен проверенный бинарник с резервной копией. Пользовательские процессы не перезапускались; live-подтверждение Ани остаётся открытым.

## Контекст

Human показывает ошибку `entry 4: activity item 1: item kind, valid ID, and text are required`. Проверка только структуры текущей проекции установила: `kind` и `text` имеют правильные типы, ID содержит `|` (U+007C).

Установленный публичный Pi provider OpenAI Responses формирует tool ID как `${item.call_id}|${item.id}`. Human exporter сохраняет этот ID и строит из него ID элементов вызова/результата. Go `validIdentifier` разрешает только Unicode letters/digits и `._:-`, поэтому отвергает корректную проекцию. Прежние fixtures использовали простые ID и не выявили несовместимость.

## Цель

Reader принимает составные ID Pi в истории и live, сохраняет точные связи вызовов/результатов и продолжает отвергать небезопасные файловые identities и некорректные проекции.

## Что изменится

1. `internal/ai-knowledge/human/reader.go`: разрешить `|` в идентификаторах проекции.
2. `internal/ai-knowledge/human/provider_id_test.go`: изолированные regression tests через `fstest.MapFS`.
3. `CONTEXT.md`: причина, исправление и фактические проверки.
4. Установленный Automata executable: после проверок пересобрать и заменить с резервной копией. Пользовательские процессы не перезапускать.

## Детали реализации

- Добавить `|` к допустимым символам `validIdentifier`. Не интерпретировать provider ID как путь и не менять его значение.
- Сохранить лимит 256 bytes, запрет пустых/dot IDs, controls, whitespace, `/`, `\\` и остальных неразрешённых символов.
- Не менять `paths.ValidateSessionID`, canonical profile validation, `Directory`, filesystem root/symlink проверки, JSON v1, grouping или классификацию ответов.
- Проверить history/live и result после explicit final с составными ID; сохранить global chronological correlation, orphan/duplicate rejection.
- Проверить, что `|` не разрешён в profile/session path components. Не переписывать пользовательские JSONL, Human JSON или модельный контекст.
- Тесты используют синтетические provider IDs и временную/подменённую FS, без реальных сессий и LLM.

## Проверки

Адресные reader/UI/root tests, reader race, `gofmt`, `go vet` и `golangci-lint`; затем полный Go suite/E2E на свежем временном бинарнике. Проверить неизменность Pi tests/typecheck и whitespace. Перед установкой сохранить старый executable, собрать новый временный файл, проверить `--help`/формат и атомарно заменить установленный бинарник. Коммиты не создавать.

## Критерии приёмки

- [x] Синтетическая проекция с `call_id|fc_id` читается в history/live без потери исходных ID.
- [x] Межцепочечная связь call/result остаётся валидной и уникальной.
- [x] Небезопасные ID и файловые identities по-прежнему отвергаются.
- [x] Тесты/race/vet/lint и итоговый Go suite проходят.
- [x] Бинарник обновлён и проверен; пользовательские процессы/история не изменены, коммитов нет.

## Фактическая проверка

Regression сначала воспроизвёл исходный отказ, затем прошёл с reader fix. Scoped root/UI/reader tests, reader race, полный module/vet/lint gate (0 issues), govulncheck (0 reachable vulnerabilities, 1 module-only advisory), полный Go suite/E2E и internal/root race прошли. Pi: 229/229, `pnpm exec tsc --noEmit`; его исходники не менялись.

Первый полный E2E запуск поймал два PTY переходных сбоя (native dialog/hide). Каждый сценарий после этого прошёл 5/5 без изменений/ослабления tests; повторный полный suite прошёл, E2E 52.479s. Первое падение не скрывается и не считается доказанно исправленным UI-дефектом.

Host/Linux amd64/macOS arm64 сборки прошли. Повторные host builds совпали. Установленный SHA-256: `f970c67fbf27352f08b0388b139a5c8432e905ce0cb69702ee6ed73f914e8ccb`. Backup прежнего binary: `/tmp/automata-provider-id-install.PJIvgW/automata.before`; installation `--help` и checksum проверены. Для использования новой версии требуется отдельный перезапуск Automata пользователем.
