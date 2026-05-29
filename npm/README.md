# quest-control-cli

Quest — spec-driven development mission orchestrator.

A TUI that orchestrates spec-driven development missions by spawning Claude Code subprocesses as workers, validators, critics, and refinement agents.

## Install

```bash
npm install -g quest-control-cli
```

## Usage

```bash
quest                     # Launch dashboard (auto-discovers specs)
quest <slug>              # Jump directly to a spec's dashboard
quest new                 # Start a new spec creation flow
quest --version           # Show version
quest --help              # Show help
```

## Supported Platforms

- macOS (Apple Silicon, Intel)
- Linux (arm64, x64)

Windows is not supported yet.

## License

MIT
