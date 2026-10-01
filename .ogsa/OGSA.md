# Også (ogsa)

A terminal coding assistant (like a lean competitor to OpenCode/PI dev), written
in Go for simplicity and to avoid npm supply-chain risk. It runs an agent loop
against an OpenAI/Anthropic-compatible chat API, giving the model file tools
(glob/grep/read/edit/write) scoped to the project directory, with a REPL UI.

## Layout

- `cmd/ogsa/main.go` — entry point. Loads config, opens an `os.Root` sandboxed
  to the cwd, wires up the REPL, sessions, and tools, then runs it.
- `internal/llm/` — provider-agnostic chat types (`llm.go`: `Message`,
  `ToolSpec`, `ToolCall`, `Request`, `Provider` interface, `HTTPError`), plus
  `client.go`/`anthropic.go`/`compat.go` implementing OpenAI-compatible and
  Anthropic HTTP clients, and `toolcheck.go` for detecting tool support.
- `internal/agent/` — the model-agnostic agent loop. `agent.go:Run` sends
  history to the provider, executes requested tools (with approval via
  `Hooks.Approve`), feeds results back, loops until the model replies with no
  tool calls or `MaxSteps` (25) is hit; detects repeated failing calls and
  gives up after 3. `prompt.go` holds the system prompt.
- `internal/tools/` — the sandboxed filesystem tools exposed to the model:
  `root.go` (`Root`/`OpenRoot`, path confinement via `os.Root`, `ReadOnly()`/
  `Mutating()` tool sets), `glob.go`, `grep.go`, `read.go`, `edit.go` (edit +
  write, with `Preview`-able diffs via `diff.go`).
- `internal/config/` — `~/.config/ogsa` JSON config: providers, selected
  provider/model, and a cache of per-model tool-support info.
- `internal/session/` — persists conversations under
  `~/.config/ogsa/projects/<name>-<hash>/sessions/` as one JSON message per
  line, keyed by a sanitized project dir name + short hash (never stored in
  the project itself).
- `internal/cli/` — the REPL (`repl.go`), slash commands (`commands.go`,
  e.g. `/model`), provider/model selection (`providers.go`), session resume
  (`sessions.go`), and project-root detection (`project.go`).
- `docs/` — design notes (`goals.md`, `r&d.md`, `developing-session.md`), not
  authoritative API docs.

## Build / run / test

- Dev run: `go run ./cmd/ogsa` (flags: `-c`/`-continue` to resume the last
  session for this project).
- Build: `go build -o ogsa ./cmd/ogsa`.
- Install to `~/.local/bin`: `./install-build.sh` (or `go install ./cmd/ogsa`).
- Test: `go test ./...` (standard library `testing`, no external deps beyond
  `golang.org/x/term`).

## Conventions

- Standard Go naming/formatting (`gofmt`); exported types documented with a
  leading-sentence doc comment (see `session.go`, `agent.go`).
- Errors wrapped with `%w` and short lowercase messages (e.g.
  `fmt.Errorf("loading %s: %w", path, err)`); sentinel errors are
  package-level vars (`ErrDeclined`, `ErrRepeatedFailure`) with a message
  aimed at the model, not just a human.
- Tools implement the small `agent.Tool` interface (`Spec()`/`Run()`);
  mutating tools additionally implement `Previewer` (`Preview()`) so the CLI
  can show a diff before asking for approval.
- All filesystem access from tools goes through `tools.Root`, which resolves
  and validates paths (`rel()`) so nothing escapes the project directory.
- Tests are plain table-free unit tests named `TestXxx`, calling `t.Fatal`/
  `t.Fatalf` directly (no testify). LLM providers are faked with small
  hand-written stubs implementing `llm.Provider` (see `scripted` in
  `agent_test.go`) rather than mocking frameworks.
- Keep provider/transport-specific code inside `internal/llm`; `internal/agent`
  and `internal/cli` should stay provider-agnostic, talking only to the
  `llm.Provider` interface and `agent.Tool`.
