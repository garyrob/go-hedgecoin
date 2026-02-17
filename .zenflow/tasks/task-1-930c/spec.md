# Technical Specification: Task 1 — Core Types and Interfaces

## Difficulty Assessment: **Easy**

This is a straightforward task involving type definitions and simple helper functions. No behavioral code, no complex logic, no integration with other systems. Pure Go type definitions that compile cleanly and pass basic unit tests.

---

## Technical Context

- **Language:** Go 1.21+
- **Codebase:** go-algorand (forked as go-hedgecoin)
- **Target Package:** `ledger/ledgercore/`
- **Dependencies:**
  - `github.com/algorand/go-algorand/crypto` (for `crypto.Digest`, `crypto.VRFVerifier`)
  - `github.com/algorand/go-algorand/data/basics` (for `basics.Round`, `basics.Address`)
  - Standard library: `errors`, `fmt`

---

## Implementation Approach

Create two new files in `ledger/ledgercore/` containing foundational types and interfaces that all subsequent tasks depend on. This follows the existing package conventions observed in `error.go` and `misc.go`.

### Design Principles (from DD 4.5 §4.2)

1. **Dependency Inversion:** Define interfaces in a low-level package (`ledgercore`) so that consensus-path packages (`agreement`, `ledger/eval`) can use typed errors without importing `node/`.

2. **Error Classification:** The `DaemonError` type allows callers to distinguish between:
   - `"internal"` errors → operational (return error)
   - `"not_found"`, `"bad_request"`, `"unsupported"` → invariant violations in eligibility-asserting paths (panic)

3. **Version Constants:** Compile-time constants ensure all nodes use identical weight derivation logic and wire format.

---

## Source Code Structure Changes

### New Files

#### 1. `ledger/ledgercore/weightoracle.go`

Contains:
- `WeightOracle` interface — the central abstraction for external weight lookups
- `DaemonIdentity` struct — metadata returned by daemon identity query
- `DaemonError` struct — typed error for daemon error responses
- `IsDaemonError()` helper — convenience function for error code checking
- Version constants:
  - `ExpectedWeightAlgorithmVersion = "1.0"`
  - `ExpectedWeightProtocolVersion = "1.0"`
- Absenteeism constant:
  - `AbsenteeismMultiplier uint64 = 20`

**Interface signature:**
```go
type WeightOracle interface {
    Weight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error)
    TotalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error)
    Ping() error
    Identity() (DaemonIdentity, error)
}
```

#### 2. `ledger/ledgercore/externalweighter.go`

Contains:
- `ExternalWeighter` interface — the interface that `*Ledger` will implement in Task 7

This is a separate interface from `WeightOracle` because:
- `WeightOracle` is the daemon client interface (used by `node/` package)
- `ExternalWeighter` is the ledger-layer interface (used by `agreement/` and `ledger/eval/` via type assertion)

**Interface signature:**
```go
type ExternalWeighter interface {
    ExternalWeight(balanceRound basics.Round, addr basics.Address, selectionID crypto.VRFVerifier) (uint64, error)
    TotalExternalWeight(balanceRound basics.Round, voteRound basics.Round) (uint64, error)
}
```

### New Test Files

#### 3. `ledger/ledgercore/weightoracle_test.go`

Unit tests for:
- `DaemonError.Error()` formatting
- `IsDaemonError()` with correct code, wrong code, non-DaemonError
- `errors.As` unwrapping through wrapped errors
- Compile-time interface satisfaction: `var _ WeightOracle = (*mockOracle)(nil)`

#### 4. `ledger/ledgercore/externalweighter_test.go`

Unit tests for:
- Compile-time interface satisfaction: `var _ ExternalWeighter = (*mockWeighter)(nil)`

---

## Data Model / API / Interface Changes

### New Types

| Type | Package | Description |
|------|---------|-------------|
| `WeightOracle` | `ledgercore` | Interface for daemon communication |
| `ExternalWeighter` | `ledgercore` | Interface for ledger weight access |
| `DaemonIdentity` | `ledgercore` | Struct with genesis hash and versions |
| `DaemonError` | `ledgercore` | Error type with Code and Msg fields |

### New Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `ExpectedWeightAlgorithmVersion` | `"1.0"` | Weight algorithm version for identity check |
| `ExpectedWeightProtocolVersion` | `"1.0"` | Wire protocol version for identity check |
| `AbsenteeismMultiplier` | `20` | Factor for absenteeism threshold calculation |

### New Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `IsDaemonError` | `func(err error, code string) bool` | Check if error is DaemonError with specific code |

---

## Verification Approach

### Build Verification
```bash
make build
```
Must compile cleanly with no errors.

### Lint Verification
```bash
make lint
```
Must pass all lint checks.

### Unit Test Verification
```bash
go test -v ./ledger/ledgercore/... -run "TestDaemonError\|TestWeightOracle\|TestExternalWeighter"
```

### Test Coverage

1. **DaemonError.Error() formatting:**
   - Test various code/message combinations
   - Verify format: `"daemon error [CODE]: MESSAGE"`

2. **IsDaemonError() behavior:**
   - Returns `true` when error is `*DaemonError` with matching code
   - Returns `false` when error is `*DaemonError` with different code
   - Returns `false` when error is not a `*DaemonError`
   - Works correctly with wrapped errors (`fmt.Errorf("wrapper: %w", daemonErr)`)

3. **errors.As unwrapping:**
   - Verify `errors.As(wrappedErr, &de)` extracts the underlying `DaemonError`

4. **Interface satisfaction (compile-time):**
   - `var _ WeightOracle = (*mockOracle)(nil)` compiles
   - `var _ ExternalWeighter = (*mockWeighter)(nil)` compiles

---

## Implementation Notes

### Copyright Header
All new files must include the standard Algorand copyright header (AGPL v3), following the pattern in existing files like `error.go`.

### Package Documentation
Add brief package-level comments to new files explaining their purpose.

### Code Style
Follow existing codebase conventions:
- Use tabs for indentation
- Error messages should be lowercase and not end with punctuation
- Interface comments should describe behavior, not implementation

### No Code Generation Required
Task 1 does not require `make generate` or `make msgp` — no msgpack-serializable types are being added.

---

## Files Summary

| File | Action | Lines (est.) |
|------|--------|--------------|
| `ledger/ledgercore/weightoracle.go` | Create | ~80 |
| `ledger/ledgercore/externalweighter.go` | Create | ~25 |
| `ledger/ledgercore/weightoracle_test.go` | Create | ~100 |
| `ledger/ledgercore/externalweighter_test.go` | Create | ~30 |

**Total estimated new code:** ~235 lines (including tests)
