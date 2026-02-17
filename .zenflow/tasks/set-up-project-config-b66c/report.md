# Project Configuration Report

## Settings Created

`.zenflow/settings.json` was configured with:

```json
{
  "setup_script": "go mod download && make libsodium",
  "verification_script": "make lint",
  "copy_files": [
    "CLAUDE.local.md",
    "*.md",
    "docs/*.md"
  ]
}
```

## Rationale

### setup_script
- `go mod download` downloads Go module dependencies
- `make libsodium` builds the libsodium cryptography library required for CGO compilation
- libsodium is required because golangci-lint needs to type-check the code, which requires CGO compilation
- Note: Building libsodium takes some time but is necessary for verification to work

### verification_script
- `make lint` runs golangci-lint with the project's `.golangci.yml` configuration
- The linter uses `new-from-rev` to only check recent changes, which helps keep runtime reasonable
- Requires libsodium to be built first (hence included in setup_script)

### copy_files
- **`CLAUDE.local.md`**: Gitignored file containing local context/instructions for Claude agents
- **`*.md` and `docs/*.md`**: Documentation files that agents need to read when processing task descriptions. Without explicitly copying these files, Zenflow agents cannot find them when referenced in task descriptions (e.g., "read AGENTS.md before implementing"). The worktree may not have these files available at the time Zenflow parses task instructions, causing agents to fail to find referenced documentation even though the files exist in git. **See "Zenflow Configuration Notes" section in CLAUDE.md for full explanation.**

### Omitted Fields
- **dev_script**: Not applicable - this is a blockchain node (algod), not a web application with a dev server
