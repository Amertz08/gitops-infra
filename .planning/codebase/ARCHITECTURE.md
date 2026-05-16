# Architecture

## System Overview

Two independent subsystems coexist in this repo:

1. **Temporal Infrastructure Worker** (`temporal/infra-worker/`) — Go service that provisions AWS networking resources using Pulumi Automation API, orchestrated via Temporal workflows. This is the active development area.

2. **GitOps Platform** (`argocd/`, `infrastructure/`, `apps/`) — Kubernetes cluster management via ArgoCD + Kustomize YAML. No Go code; entirely declarative. Manages the Kubernetes side of the infrastructure.

The two subsystems are currently independent — the Temporal worker provisions VPCs/TGW/VPN at the AWS level; ArgoCD manages workloads that run on top.

---

## Temporal Infrastructure Worker

### Pattern: Workflow → Child Workflow → Activity → Pulumi Inline Program

Every AWS resource is provisioned through a strict four-layer stack:

```
InfraDeployWorkflow (top-level orchestrator)
  └─ VpcWorkflow (child) × 3 [parallel]
       ├─ CreateVpc (activity)
       ├─ CreateIgw (activity)
       ├─ CreateSubnet (activity) × N [parallel]
       ├─ NatGatewayWorkflow (child) × N [parallel]
       │    ├─ CreateEip (activity)
       │    └─ CreateNatGateway (activity)
       └─ RouteTableWorkflow (child) × N [parallel]
            ├─ CreateRouteTable (activity)
            ├─ AddRoute (activity) × M [sequential]
            └─ AssociateSubnet (activity) × K [parallel]
  └─ TgwWorkflow (child) [sequential, after VPCs]
       ├─ CreateTgw (activity)
       └─ CreateVpcAttachment (activity) × N [parallel]
  └─ VpnWorkflow (child) [sequential, after TGW]
       └─ ... VPN activities
```

Each activity wraps a single call to `auto.UpsertStackInlineSource` + `stack.Up()` — one Pulumi stack per AWS resource. This makes each resource independently addressable, retryable, and auditable in Pulumi state.

### Fan-out / Fan-in Pattern

Parallelism is expressed with `workflow.Go` + channels (top-level) or `workflow.ExecuteActivity`/`workflow.ExecuteChildWorkflow` returning `Future` slices (within workflows):

```go
// Fan-out
futures := make([]workflow.Future, len(items))
for i, item := range items {
    futures[i] = workflow.ExecuteActivity(ctx, act, item)
}
// Fan-in
for i, f := range futures {
    f.Get(ctx, &results[i])
}
```

Channel-based fan-in is used at the top level (`InfraDeployWorkflow`) to drain all VPC goroutines before error propagation.

### Query Handler

`InfraDeployWorkflow` registers a `"status"` query handler that returns live phase progress:
```go
workflow.SetQueryHandler(ctx, "status", func() (InfraStatus, error) { ... })
```
Phase transitions: `vpc` → `tgw` → `vpn` → `done` (or `failed`).

### Pulumi Automation API Pattern

Activities use the **inline program** pattern — no `Pulumi.yaml` files, no CLI:
- `auto.UpsertStackInlineSource(ctx, stackName, projectName, pulumiRunFunc)`
- One stack per AWS resource; stack names are derived from the workflow input (e.g. `"ops-vpc"`, `"ops-vpc-igw"`, `"ops-vpc-nat-0"`)
- State stored on local filesystem (no remote backend configured)

### Heartbeat Strategy

Two mechanisms prevent Temporal from timing out long-running Pulumi provisioning calls:
1. A 30-second ticker goroutine in `upStack()` sends keepalive heartbeats
2. `heartbeatWriter` streams Pulumi progress output as heartbeat details

---

## GitOps Platform (ArgoCD)

### App-of-Apps Pattern

`argocd/root.yaml` is the root ArgoCD Application pointing at `argocd/apps/`. Each file in `argocd/apps/` is an ArgoCD Application managing one concern:
- Infrastructure add-ons: `cert-manager`, `envoy-gateway`, `sealed-secrets`
- ArgoCD itself (self-managing): `argocd.yaml`
- App deployments: `apps-auto-dev.yaml`, `apps-auto-qa.yaml`, `apps-prod.yaml`

### PR Preview Environments

`team-a-example-app-preview.yaml` uses ArgoCD's PR Generator to create ephemeral preview environments for each open PR against `Amertz08/example-app`. Auth via `github-token` Kubernetes Secret.

### Kustomize Overlay Structure

```
apps/team-a/example-app/
  base/           # Deployment + Service
  overlays/
    dev/          # Kustomize patch for dev
    qa/           # Kustomize patch for qa
    prod/         # Kustomize patch for prod
```

Infrastructure add-ons follow the same pattern under `infrastructure/`.

---

## Key Architectural Decisions

| Decision | Choice | Implication |
|---|---|---|
| IaC tool | Pulumi Automation API (inline) | No YAML files; Go code only; one stack per resource |
| Orchestration | Temporal | Durable execution; automatic retries; queryable state |
| State backend | Local filesystem | No team collaboration on state; must be addressed for production |
| Parallelism | Temporal futures + channels | Deterministic, replay-safe concurrency |
| K8s delivery | ArgoCD GitOps | Declarative, self-healing; no imperative kubectl |
