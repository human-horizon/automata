# Аудит process identity на macOS и Linux

## Зафиксированное поведение

- `PrepareKillSessionForProfile` проверяет все job records до сигналов; неизвестная identity отклоняется.
- Текущая проверка делает `kill(pid, 0)`, затем читает `ps -p <pid> -o lstart=` и сравнивает результат с job `startedAt`.
- `ps lstart` имеет секундную точность; текущий допуск — 5 секунд для известных различий времени. `PIDUnknown` fail-closed для destructive shutdown.
- `ExecuteKillPlansForProfile` повторно проверяет identity непосредственно перед SIGTERM и перед SIGKILL; ожидание завершения ограничено.

## Вывод аудита

Сильнее сопоставлять PID со start time на Linux можно через `/proc/<pid>/stat` и/или pidfd, но producer сейчас не сохраняет соответствующий kernel token. pidfd также не является общей реализацией для macOS. В имеющемся cross-platform контракте не доказана безопасная операция «сигнализировать только если это всё ещё тот же process», поэтому сужать/расширять допуск, заменять identity проверку или добавлять fallback нельзя.

Runtime-код не меняется. Существующая проверка перед сигналом и fail-closed отказ при неизвестной identity остаются обязательными; остаточный PID reuse/TOCTOU риск должен быть явно обозначен, а не скрыт обещанием абсолютной identity.

## Условия для будущего усиления

- Producer сохраняет точный process identity token, согласованный с Go consumer schema.
- Linux signal backend привязывает проверенный process handle к последующим signals; для macOS доказан эквивалентный безопасный контракт либо система отказывает закрыто.
- Regression tests моделируют PID reuse между prepare, SIGTERM и SIGKILL, а также неизвестные/повреждённые identity данные.
- Проверены macOS и Linux builds/tests; ни одна ошибка identity не приводит к fallback на PID-only kill.
