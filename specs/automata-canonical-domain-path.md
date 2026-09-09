# Канонические пути доменов Automata

## Контекст

`internal/ai-knowledge/memory/reader.go` и `ContextPanel.domainDirForActive` в
`internal/ui/context_panel.go` отдельно разрешают профиль и собирают путь к
домену. Эти реализации используют явно переданное имя профиля как имя папки,
вместо общего канонического преобразования `paths.ProfileSlug`.

Для профиля `Проект Ω` канонический slug — `proekt-ω`, но Content и watcher
сейчас ищут данные в пути с сырым именем `Проект Ω`. Поэтому Content не читает
канонические notes, а watcher не видит их изменения. Kanban уже использует
`paths.DomainDir` и работает с каноническим путём.

При исправлении нельзя потерять действующую семантику пустого профиля:
сначала используется `AI_PROFILE`, а если он также пуст — профиль `default`.

## Цель

Сделать `paths.DomainDir` единственным построителем пути домена для memory
reader и watcher `ContextPanel`, сохранив `AI_DATA_HOME`, канонический slug
профиля и семантику `пустой profile → AI_PROFILE → default`.

## Файлы в scope

- `internal/ai-knowledge/memory/reader.go` — заменить локальную сборку пути к
  `notes.json` на `paths.DomainDir`; удалить ставшие ненужными локальные
  resolver-ы, slugify и построение base path.
- `internal/ai-knowledge/memory/reader_test.go` — добавить проверки профиля,
  окружения и отсутствия legacy raw-пути.
- `internal/ui/context_panel.go` — направить путь watcher-а через
  `paths.DomainDir`; не дублировать сборку каталогов.
- `internal/ui/context_panel_test.go` — проверить канонический путь watcher-а
  и обновление Content.
- `internal/ui/container.go`
- `internal/ui/container_test.go`
- `CONTEXT.md` — добавить запись о проблеме и решении в формате
  `[ДАТА] Проблема: X → Решение: Y`.

Явно исключены: `extensions/automata/index.ts`, любая миграция,
`AUTOMATA_HOME`, UI shortcuts F5/F7 и временные `/tmp` mouse logs — для них
нужна отдельная спецификация.

## Детали реализации

1. В обоих runtime-участках использовать `paths.DomainDir(profile, domain)`;
   не собирать вручную `dataHome/profiles/<profile>/domains/<domain>` и не
   поддерживать локальный алгоритм slugify.
2. Перед вызовом `paths.DomainDir` сохранить эффективный профиль: непустой
   явно переданный профиль имеет приоритет; при пустом profile читается
   `AI_PROFILE`; если переменная также пуста, в `paths.DomainDir` передаётся
   пустое значение, чтобы `paths.ProfileSlug` выбрал `default`. Допустим
   только минимальный helper для этого разрешения, если он необходим; новый
   builder пути или второй slugger не добавлять. `RefreshKnowledgeCmd` читает
   memory и при пустом profile, используя то же разрешение эффективного профиля.
3. Путь `notes.json` получать только добавлением имени файла к результату
   `paths.DomainDir`; watcher создавать на этот же каталог. `AI_DATA_HOME`
   должен разрешаться исключительно каноническим `paths.BaseDir` внутри
   `paths.DomainDir`.
4. В тестах изолировать окружение через временный `AI_DATA_HOME` и
   `t.Setenv`; отдельно покрыть:
   - пустой профиль без `AI_PROFILE` и профиль `default`;
   - пустой profile с `AI_PROFILE`;
   - заданный `AI_DATA_HOME` при отличающемся домашнем каталоге;
   - явный Unicode/Cyrillic-профиль `Проект Ω` с ожидаемым путём
     `profiles/proekt-ω/domains/<domain>`;
   - watcher, который реагирует на запись canonical `notes.json`;
   - отсутствие чтения/создания legacy raw-пути
     `profiles/Проект Ω/domains/<domain>`.
5. Не менять жизненный цикл watcher-а, Content/Kanban или другие перечисленные
   вне scope функции. После реализации обновить только одну запись в
   `CONTEXT.md`.

## Критерии приёмки

- [x] Memory reader читает notes из `paths.DomainDir` для default-профиля.
- [x] Пустой profile использует `AI_PROFILE`, а при пустом `AI_PROFILE` —
      `profiles/default`.
- [x] При заданном `AI_DATA_HOME` ни reader, ни watcher не используют другой
      base path.
- [x] Профиль `Проект Ω` разрешается через `paths.ProfileSlug` в
      `proekt-ω`; canonical notes читаются и отображаются.
- [x] ContextPanel watcher установлен на canonical domain directory и видит
      изменения `notes.json` без ручного `Refresh`.
- [x] Legacy raw-путь `profiles/Проект Ω/...` не читается и не создаётся.
- [x] В runtime-коде нет дублирующей сборки domain path или локального slugger-а.
- [x] `gofmt` не находит неформатированных Go-файлов.
- [x] `go vet ./...` завершается успешно.
- [x] `go test ./... -count=1 -p 1` завершается успешно.
- [x] `go build -o automata .` завершается успешно.
- [x] Scoped `git diff --check` завершается успешно; изменены только файлы из
      разрешённого scope.

## Состояние

Спецификация одобрена; runtime- и test-реализация выполнены в указанном scope.
