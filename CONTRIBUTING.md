# Contributing

Thanks for helping improve Automata. Keep changes focused, preserve existing user data and fail-closed safety checks, and add regression tests for behavior changes.

## Before opening a pull request

1. Read the relevant code and specifications under `specs/`.
2. Format Go files with `gofmt` and keep changes free of unrelated generated files, local data, and secrets.
3. Run the repository checks from the project README:

   ```sh
   go mod verify
   test -z "$(gofmt -l .)"
   go vet ./...
   golangci-lint run --timeout=5m ./...
   go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
   go test ./... -count=1 -p 1
   go test -race ./internal/... . -count=1 -p 1
   git diff --check
   ```

4. For changes to terminal behavior, also run the relevant E2E tests and include any required environment setup from `.github/workflows/ci.yml`.
5. Describe user-visible behavior, compatibility or migration impact, and the tests actually run. Do not claim checks that were not run.

The supported development platforms and Go version are documented in `README.md`. Do not commit user session data, credentials, build output, or unrelated worktree changes.
