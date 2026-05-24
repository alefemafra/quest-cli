# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Mission Dashboard — a Go TUI (Bubbletea) that orchestrates spec-driven development missions. It spawns Claude Code subprocesses as workers, validators, critics, and refinement agents, coordinating them through a phased execution pipeline. The binary is called `mission`.

## Build & Run

```bash
go build -o mission .          # compile
go run .                       # run from source
go run . <slug>                # jump directly to a spec's dashboard
go run . new                   # force new spec creation flow
go vet ./...                   # lint
```

No test suite exists yet.

## Architecture

### Execution Pipeline

The TUI drives a multi-phase pipeline with 5 agent roles, all spawned as `claude` CLI subprocesses:

1. **Spec Discovery** — interactive chat with Claude to gather requirements, produces a plan JSON
2. **Critic Gate** — mechanical checks (Node.js script) + judgment checks (Claude) validate the plan before workers start
3. **Workers** — one Claude subprocess per feature, running in parallel within a phase, sequential across phases
4. **Validators** — black-box validation of each completed feature against assertion contracts
5. **Refinement** — on validation failure, generates fix features that re-enter the pipeline

### Phase Model

Features are grouped into phases (0=Foundation, 1=Core, 2=Polish, 3=Extras). All features in a phase run concurrently; the next phase starts only when the current one completes. Dependencies within a phase gate individual feature starts.

### Status Lifecycle

`pending → in_progress → awaiting_validation → validating → done`
Branch: `validating → refining → (fix features created) → blocked`
Error: `→ blocked`

Workers do NOT manage their own status in features.json — the orchestrator (WorkerPool) handles all transitions.

### Key Files

- `main.go` — CLI entry point, argument parsing
- `internal/app.go` — Bubbletea Model, all views (spec select, chat, review, dashboard), key handling
- `internal/worker.go` — WorkerPool: concurrency, retries, phase advancement, validator/refinement orchestration
- `internal/claude.go` — Claude subprocess management, stream-JSON parser, all prompt builders
- `internal/mission.go` — spec scanning, features.json R/W, plan parsing, mission file generation
- `internal/context.go` — auto-detects project stack/architecture/deps/routes for prompt context
- `internal/validator.go` — validator prompt + assertion filtering + report parsing
- `internal/critic.go` — mechanical checks (embedded Node script) + judgment prompt + report parsing
- `internal/refinement.go` — refinement prompt + fix feature parsing + features.json mutation
- `internal/skills.go` — `embed.FS` for skill markdown files (read via `ReadSkill("name")`)
- `internal/skills/` — embedded skill docs fed into Claude prompts (mission-spec, mission-worker, etc.)
- `internal/skills/checks/run-mechanical.mjs` — embedded Node script for structural validation

### Mission Files (per spec)

Specs live at `docs/specs/<slug>/` in the target project. The mission subfolder:
```
docs/specs/<slug>/
├── spec.md
├── mission/
│   ├── features.json          # manifest: features, fix_features, lifecycle
│   ├── validation-contract.md # behavioral assertions
│   ├── knowledge-base.md      # append-only findings from workers/validators
│   ├── project-context.md     # cached auto-detected project snapshot
│   ├── runs/                  # per-feature validator/critic JSON reports
│   └── logs/                  # orchestrator.log + per-feature logs
└── designs/
```

### Concurrency Model

WorkerPool uses a `sync.Mutex` for worker state and a separate `sync.Mutex` (`fileMu`) for features.json writes. Workers communicate via a buffered `chan WorkerEvent`. The pool runs goroutines for each worker/validator/refinement agent and advances phases when all features in a phase reach terminal state.

### Claude Subprocess Interface

All agent roles use `StartClaude()` which spawns `claude -p <prompt> --output-format stream-json --verbose`. The stream parser extracts tool calls, text blocks, and results from the JSON stream. Session IDs are captured for `--resume` on transient failures.

### Retry Strategy

- Workers: 3 retries (with session resume when possible), 5 for transient errors (socket/rate-limit)
- Validators: 2 retries
- Refinement: 3 rounds max before escalating to blocked
- Phase-level: 1 retry of all failed features per phase (with failure context injection)
