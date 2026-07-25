# Запуск just-pi при открытии чата

## Контекст
При открытии чата в Automata должен запускаться `just-pi` с параметром `--session-id <name>`, где `<name>` — уникальное slugified имя сессии.

## Цель
Автоматически стартовать `just-pi` для выбранного чата и передавать ему корректный session-id.

## Поведение

### Команда
```go
cmd := "just-pi"
args := []string{"--session-id", sessionName}
```

### sessionName
- Базовое имя: `slug.SessionName(item.Path(), item.Name)`.
- С профилем: `slug.Slug(profile) + "__" + sessionName`.
- Пример без профиля: `users.a.space.projects.humanhorizon.automata.segodnya.mychat`.
- Пример с профилем `Human Horizon`: `human-horizon__users.a.space.projects.humanhorizon.automata.segodnya.mychat`.

### Override для тестов
- Переменная окружения `PI_CMD` заменяет команду (и аргументы) для тестов.
- Пример: `PI_CMD=/bin/bash` запускает shell вместо `just-pi`.

### Flow
1. Пользователь открывает чат (Enter/клик).
2. `Tree` вызывает `onSelectChat`.
3. `SessionManager.openChat`:
   - ищет существующий `Emulator` по `ComposeSessionID`
   - если нет — создаёт новый с `cmd`/`args`
4. `App.Update` получает `ChatSelectedMsg` и вызывает `em.Start()`.
5. `Emulator.Start` спавнит PTY и начинает слушать вывод.

## Критерии приёмки
- [x] Открытие чата запускает `just-pi --session-id <name>`.
- [x] Имя сессии корректно slugified.
- [x] Профиль добавляет slugified префикс.
- [x] `PI_CMD` позволяет переопределить команду для e2e-тестов.
- [x] `go test ./...`, `go vet ./...`, `go build` проходят.
