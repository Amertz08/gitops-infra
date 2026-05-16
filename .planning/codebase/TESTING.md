# Testing

## Current State: No Tests

There are **zero test files** (`*_test.go`) anywhere in the repository. No test suite exists.

## Evidence
- `find . -name '*_test.go'` returns nothing
- No `Makefile` with test targets
- No CI pipeline configuration (`.github/workflows/`, etc.)
- No coverage tooling configured

## Test Dependencies
- `github.com/stretchr/testify v1.11.1` appears in `go.mod` as an **indirect** dependency
- It is pulled in transitively — not used directly in any test file
- This suggests tests were planned or started but never written

## What Is Not Tested
- All Temporal workflows (`workflows/` package)
- All Temporal activities (`activities/` package)
- Pulumi Automation API inline programs
- Worker registration and startup (`main` packages)

## Temporal Testing Capability (Available, Unused)
- The Temporal Go SDK ships `go.temporal.io/sdk/testsuite` for unit-testing workflows and activities without a live server
- This would allow replay-safe determinism tests and activity mock injection
- Not currently used

## Risk
All logic runs untested in production. Any change to workflow or activity code has no automated safety net. See `CONCERNS.md` for severity assessment.
