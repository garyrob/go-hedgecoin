# Project Configuration Report

## Settings Created

`.zenflow/settings.json` was configured with:

```json
{
  "setup_script": "go mod download",
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
- Full build (`make build`) requires system dependencies (libsodium) and takes too long for worktree setup

### verification_script
- `make lint` runs golangci-lint, the primary quick check from CI
- Should complete in under 60 seconds for typical changes

### copy_files
- **`CLAUDE.local.md`**: Gitignored file containing local context/instructions for Claude agents
- **`*.md` and `docs/*.md`**: Documentation files that agents need to read when processing task descriptions. Without explicitly copying these files, Zenflow agents cannot find them when referenced in task descriptions (e.g., "read AGENTS.md before implementing"). The worktree may not have these files available at the time Zenflow parses task instructions, causing agents to fail to find referenced documentation even though the files exist in git.

### Omitted Fields
- **dev_script**: Not applicable - this is a blockchain node (algod), not a web application with a dev server
