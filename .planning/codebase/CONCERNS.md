# Concerns

## HIGH Severity

### H1 — Nil pointer receiver for activity dispatch
**Files:** All workflow files (`workflows/vpc.go`, `workflows/tgw.go`, `workflows/sg.go`, etc.)

Activities are called via a nil pointer receiver:
```go
var acts *activities.InfraActivities
workflow.ExecuteActivity(ctx, acts.CreateVpc, ...)
```
This compiles and works because Temporal uses reflection to resolve the function reference — but it is fragile and will panic if any code path dereferences `acts` directly inside the workflow. Should use a named function reference instead.

### H2 — No workflow execution timeout
`InfraDeployWorkflow` has no `WorkflowExecutionTimeout` set on the `StartWorkflowOptions` in `starter/main.go`. A stuck or hung `pulumi up` call will block the workflow forever, consuming a Temporal worker slot indefinitely.

### H3 — Fixed workflow ID blocks concurrent runs
`starter/main.go` hardcodes `ID: "infra-deploy-main"`. Temporal rejects new workflow starts with a duplicate ID (default policy). Any attempt to re-run while a previous run is open (including after a failed run that hasn't been terminated) will fail.

### H4 — Local Pulumi state backend
All stacks use `auto.UpsertStackInlineSource` with no backend configured, defaulting to the local filesystem (`~/.pulumi/`). State is lost on pod/container restart and is not shareable across multiple worker instances. Running two workers simultaneously will corrupt state.

### H5 — No concurrency guard on Pulumi stacks
Multiple workflow executions can invoke `upStack` with the same `stackName` simultaneously. Pulumi does not lock inline stacks against concurrent `Up` calls, leading to state corruption.

### H6 — Silent nil output passed as resource ID
`fmt.Sprintf("%v", result.Outputs["key"].Value)` returns the string `"<nil>"` when a Pulumi output key is missing. This string is then used as a resource ID (e.g., `VpcId`, `SubnetId`) in downstream activities, causing cryptic AWS API errors rather than a clear failure.
**Files:** `activities/vpc.go`, `activities/tgw.go`, `activities/sg.go`, `activities/vpn.go`

### H7 — VpcWorkflow panics on AZ/subnet count mismatch
`VpcWorkflow` indexes into `input.Azs[i]` for each subnet CIDR without bounds-checking. If `len(Azs) < len(PrivateSubnetCidrs)`, the workflow panics with an index-out-of-range error.
**File:** `workflows/vpc.go`

### H8 — VPN connection logging disabled
The Client VPN activity explicitly disables connection logging. No audit trail for VPN access events — a security and compliance gap.

### H9 — VPN authorizes all groups to entire RFC-1918 /8
The VPN authorization rule grants `authorizedCidr: "10.0.0.0/8"` to all groups. Any VPN client can reach all three VPC environments (ops, qa, prod) without group-based access control.

### H10 — Zero test coverage
No `*_test.go` files exist anywhere in the repository. All provisioning logic, retry handling, fan-out/fan-in, and error propagation is untested. The Temporal Go SDK ships `testsuite` for deterministic workflow unit tests — it is unused.

---

## MEDIUM Severity

### M1 — Heartbeat flooding under heavy Pulumi output
`heartbeatWriter.Write` sends a Temporal heartbeat for every byte slice written by Pulumi's progress streamer. Under verbose output, this can send thousands of heartbeats per second, adding unnecessary load to the Temporal server.
**File:** `activities/common.go`

### M2 — No retry backoff configuration
All retry policies use `MaximumAttempts: 2` with no `InitialInterval`, `BackoffCoefficient`, or `MaximumInterval`. AWS API rate limits and transient errors will retry immediately, increasing the likelihood of consecutive failures.

### M3 — No non-retryable error classification
All errors from `upStack` are treated as retryable. Permanent failures (e.g., invalid CIDR, missing IAM permission, non-existent ACM cert ARN) will be retried until `MaximumAttempts` is exhausted, wasting time and potentially causing resource drift.

### M4 — Dead code: `extractStringSlice`
`activities/common.go` defines `extractStringSlice` but it is never called anywhere in the codebase.

### M5 — `RouteSpec` zero-value creates invalid route
`RouteSpec` has three optional target fields (`GatewayId`, `NatGatewayId`, `TransitGatewayId`). If all are empty, `AddRoute` creates a route with no target — an invalid AWS API call. No validation guards against this.
**File:** `activities/vpc.go`

### M6 — AZ construction assumes AWS naming convention
`InfraDeployWorkflow` constructs AZ names as `region + "a"`, `region + "b"`, `region + "c"` (e.g., `"us-east-1a"`). This breaks for regions with non-standard AZ naming and does not verify the AZs actually exist in the target account.
**File:** `workflows/deploy.go`

### M7 — No TLS on Temporal client
Both `worker/main.go` and `starter/main.go` connect to Temporal with no TLS configuration. In any non-localhost deployment, workflow inputs (which include ACM cert ARNs and AWS region) travel unencrypted.

### M8 — TGW default route table association
The Transit Gateway is created without disabling the default route table association. Any future VPC attached to this TGW will automatically participate in east-west routing between all environments unless explicitly removed.

### M9 — No structured logging or metrics
All logging uses `log.Printf` (worker startup) or `workflow.GetLogger` (workflows). No structured fields, no log levels, no metrics exported to Prometheus/CloudWatch. No alerting on workflow failures.

### M10 — Starter blocks indefinitely on `we.Get()`
`starter/main.go` calls `we.Get(context.Background(), &result)` with no timeout. If the workflow hangs (see H2), the starter process hangs too with no way to interrupt other than SIGKILL.

### M11 — Broken preview ApplicationSet image reference
`argocd/apps/team-a-example-app-preview.yaml` uses ArgoCD's PR Generator image updater pattern but references a container registry path that may not exist or may use a placeholder. Preview environments will fail to deploy images.

### M12 — Example app has no resource limits
`apps/team-a/example-app/base/deployment.yaml` defines no CPU or memory `limits`. Pods can consume unbounded node resources.

---

## LOW Severity

### L1 — No teardown workflow
There is no workflow or activity to destroy Pulumi stacks (`stack.Destroy()`). Decommissioning environments requires manual `pulumi destroy` invocations with knowledge of all stack names.

### L2 — No graceful shutdown timeout
`worker/main.go` uses `worker.InterruptCh()` with default options. No `StopTimeout` is set, so in-flight activities may be abruptly interrupted on SIGTERM rather than completing their current heartbeat cycle.

### L3 — Self-signed / internal certificate for ArgoCD
`argocd/install/certificate.yaml` references a `ClusterIssuer`. If the issuer is self-signed or internal, browser clients and CI tooling will reject the certificate without explicit trust configuration.

### L4 — `golang.org/x/mock` indirect dependency is archived
`go.sum` includes `golang.org/x/mock`, which has been archived upstream. The module continues to work but will not receive security patches.

### L5 — Go version listed as `1.25.8` in `go.mod`
Go 1.25 does not yet exist (as of mid-2026, Go 1.23 is current). This may be a typo for `1.23.8` or `1.21.8` and could cause toolchain resolution issues depending on the Go toolchain in use.
