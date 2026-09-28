# Automata

Automata is a terminal workspace for organizing persistent folders, AI chats, and shell terminals in one Bubble Tea UI. It combines a tree workspace with Warp/Portalis terminal panels, session restoration, profile-scoped state, context/knowledge views, Kanban tasks, and background job tracking.

## Platform status

- **macOS:** supported and exercised by local development/E2E testing.
- **Linux:** supported and exercised by GitHub Actions.
- **Windows:** not currently supported. Job identity/termination uses Unix process semantics (`ps` and signals), and the current PTY dependency also relies on Unix terminal signals.

The platform-specific file opener includes a Windows command, but that alone does not make the full application Windows-compatible.

## Requirements

- Go **1.26.6** or newer compatible 1.26.x toolchain.
- A supported terminal on macOS or Linux.
- `pi` or `just-pi` on `PATH` for AI chat sessions, unless `PI_CMD` points to the executable.

## Build and run

```sh
go build -trimpath -buildvcs=false -o automata .
./automata
```

Useful options:

```text
--profile NAME           isolate workspace state by profile
--pi TAG                 select ~/.ai/<tag>/pi (default: just)
--scrollback-lines N     terminal scrollback limit; 0 means unlimited
--debug-log PATH         append debug logs
--cpuprofile PATH        write a CPU profile
--memprofile PATH        write a heap profile on shutdown
```

Environment variables:

- `AI_DATA_HOME` — override the default Automata data root.
- `PI_CMD` — explicit `pi` executable override.

## State and privacy

By default Automata stores state under:

```text
~/.ai/automata/profiles/<profile>/
```

Automata-owned data directories are hardened to `0700` and state/metadata files to `0600`. State can include terminal working directories and command history, so it should be treated as private user data.

## Development checks

The CI quality gate runs formatting, module verification, vet, golangci-lint, vulnerability scanning, tests, race tests, E2E coverage, clean-tree checks, and reproducible builds.

Equivalent local checks:

```sh
go mod verify
test -z "$(gofmt -l .)"
go vet ./...
golangci-lint run --timeout=5m ./...
govulncheck ./...
go test ./... -count=1 -p 1
go test -race ./internal/... . -count=1 -p 1
git diff --check
```

The E2E suite may build and launch an Automata test binary. Tests isolate `HOME`/`AI_DATA_HOME` from normal user state.

## Repository layout

- `main*.go` — application orchestration, lifecycle, overlays, input/update routing.
- `internal/tree` — persistent workspace tree and interaction model.
- `internal/ui` — chat, terminal, knowledge, context, and Kanban panels.
- `internal/ai-knowledge` — profile-scoped context, memory, job, and rendering readers.
- `internal/atomicfile` — durable atomic file replacement helpers.
- `internal/paths` — canonical on-disk layout and session migration helpers.
- `e2e` — terminal-level integration tests.
- `specs` — implementation and hardening specifications.

## Security baseline

Destructive job actions validate process identity before signalling, persistent state uses atomic writes, corrupt state is not silently overwritten, and CI runs `govulncheck`. Session IDs are validated before being used as path components.
