# AGENTS.md

Baton is a single Go binary (`baton`) that orchestrates Claude Code and Codex. Read `GLOSSARY.md` for the vocabulary, `DESIGN.md` for decisions (formats and layouts are recorded there), `docs/adr/` for hard-to-reverse choices, and `.scratch/baton-v1/` for the spec and tickets.

All repository files (code, comments, docs) are in English.

## Layout and layering

Module `github.com/loadkpi/baton`, Go 1.24, `CGO_ENABLED=0`. Structure and approach follow ardanlabs/service, adapted to a CLI (no HTTP, DB or k8s).

```
app/baton/                                  main and command wiring: flags, output, exit codes
business/domain/<name>bus/                  domain logic, one package per domain
business/domain/<name>bus/stores/<name>file/ file-backed storage of that domain
business/types/<type>/                      strong types (validated on Parse); used by the business layer only
foundation/<pkg>/                           generic packages that know nothing about Baton
internal/batontest/                         the test harness (see Tests)
test/                                       the tests, all at the one seam
```

- Create a layer or package only when a ticket needs it.
- A domain package defines the `Storer` interface it needs; the store implements it. `app` wires them together.
- Prefer the standard library. A third-party dependency needs a reason, recorded in the commit or `DESIGN.md`.

### Type boundaries and converters

Primitive types live at the edges (`app` flags and output, file/JSON/TOML document structs). Strong types from `business/types/*` live only in the business layer. Every boundary crossing goes through a named converter; never assign across a boundary directly:

| Direction | Name | Notes |
| --- | --- | --- |
| app to business | `toBus<Type>` | parses and validates; returns an error |
| business to app | `toApp<Type>` | converts strong types to primitives explicitly |
| business to file | `toFile<Type>` | |
| file to business | `toBus<Type>` | parses native values into strong types; returns an error |

## Tests

There is exactly one seam: the built `baton` executable, run as a subprocess against an isolated `HOME` and a temporary git repository. Do not write tests against internal Go packages. If a change seems to need another seam, stop and raise it instead of inventing one.

- `test/main_test.go` builds the binary once per test run (`TestMain`). Each test starts with `env := newEnv(t)`.
- `internal/batontest` provides `Env`: isolated `HOME`, a source git repository (`SourceFile`, `CommitSource`), Profile directories, `WriteConfig`, `Run` / `RunWithStdin`, file helpers (`ReadFile`, `Exists`, `ListDir`, `Mode`, `ModTime`).
- The outside world is stubbed on `PATH`. Stubs for `guake`, `docker`, `notify-send`, `claude` and `codex` are installed in every `Env` and record their calls; read them with `env.Calls(name)`, customize with `env.Stub(name, batontest.WithScript(...))`. Add stubs for new external programs in the same way. Git is real. Telegram is a local HTTP server.
- Processes started by an `Env` get an environment built from scratch (no inherited variables, no `XDG_*`, no git config). Run other programs with `env.Command`, which resolves them against the Env's `PATH`, never the test process's.
- Questions to the user are tested through a pseudo-terminal: `env.RunTerminal(replies, args...)` types one reply per `[y/N] ` prompt, `env.StartTerminal` gives `Expect` / `Send` / `Wait` for anything less regular. The pseudo-terminal is made with the standard library (Linux only), so there is no pty dependency. "No terminal" is `env.RunWithStdin`, whose stdin is a pipe.
- Assert on external behavior only: stdout, stderr, exit code, files, stub calls. Expected values are literals, not recomputed the way the code does it.
- Work test first, in vertical slices: one failing test, the minimal code to pass, next test. Refactor at the end.

## Safety rules

- Never read or write the real `~/.claude*`, `~/.codex`, `~/.baton`, `~/.config/baton` or any real Profile. Never run the built `baton` against the real `HOME`. Tests and manual experiments use temporary directories only.
- Never launch real `claude`, `codex`, `guake`, `docker run`, `notify-send` or Telegram from tests; stubs only.
- No global installs (`go install`, `npm i -g`, `apt`, ...) without asking. Module dependencies via `go get` need a justification.
- No production systems of any kind.
- `baton sync` must never write a file that git tracks and must never change or delete a file, block or key that is not in its manifest (see `DESIGN.md`, D4). Secret values are never printed: not in diffs, not in errors.

## Make targets

| Target | What it does |
| --- | --- |
| `make build` | static binary to `bin/baton` |
| `make test-only` | `go test ./...` |
| `make lint` | `golangci-lint run` |
| `make fmt` | `gofmt -w .` |
| `make vet` | `go vet ./...` |
| `make test` | `test-only` and `lint` |

While working, run `go vet ./...` and single tests (`go test ./test/ -run TestName`) often; run the full suite once at the end.

## Decisions

When a ticket leaves a decision "to implementation", make it and record it briefly in `DESIGN.md` in its existing style. Facts that were not verified are marked `[unverified]`.
