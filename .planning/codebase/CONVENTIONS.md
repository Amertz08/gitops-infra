# Conventions

## Package Naming
- Package names match directory names: `activities`, `workflows`, `main`
- No aliasing or abbreviation — direct one-to-one with filesystem layout

## Type Naming
- Per-activity I/O: `Create<Resource>Input` / `Create<Resource>Output`
- Top-level workflow I/O: `<Resource>Input` / `<Resource>Outputs`
- Consistent suffix pattern makes type purpose immediately clear

## Struct Conventions
- All input structs passed by value (not pointer)
- Full JSON tags on all fields: camelCase, `omitempty` on optional fields
- Example: `json:"vpcId,omitempty"`

## Error Handling
- Immediate return on first error — no error accumulation
- `temporal.NewApplicationError` used for domain-level failures inside workflows/activities
- No custom error types beyond Temporal's SDK error wrapping

## Context Management
- `shortCtx` (5-minute timeout) and `longCtx` (20-minute timeout) declared at top of each workflow
- Both reused throughout the workflow body rather than creating new contexts per activity call

## Logging
- `log.Fatalln` / `log.Printf` used in `main` packages only
- `workflow.GetLogger(ctx)` used inside workflows and activities — never `log.*` in workflow code
- Keeps Temporal's replay-safe logging enforced at the boundary

## Documentation
- Workflow functions have doc comments that list numbered steps in a code block
- Activity functions are generally undocumented

## Git Commit Style
- Imperative `Verb Subject: detail` format
- Examples from log:
  - `Singularize CreateSubnets: one subnet per activity call, loop in workflow`
  - `Split CreateNatGateways into EIP + NatGateway activities; introduce NatGatewayWorkflow`
  - `Introduce SecurityGroupWorkflow; split CreateSecurityGroup into focused activities`
- Subject describes the structural change; detail describes the motivation or mechanism

## Configuration
- Environment variables used for runtime config (`TEMPORAL_HOST_PORT`, `AWS_REGION`)
- No config file or flag parsing — kept minimal
