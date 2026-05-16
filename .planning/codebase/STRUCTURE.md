# Structure

## Repository Layout

```
gitops-infra/
├── temporal/infra-worker/        # Go module: Temporal worker + Pulumi provisioning
│   ├── go.mod                    # Module: github.com/adammertz/gitops-infra/temporal/infra-worker
│   ├── go.sum
│   ├── worker/
│   │   └── main.go               # Entry point: starts Temporal worker, registers all workflows+activities
│   ├── starter/
│   │   └── main.go               # CLI entry point: submits InfraDeployWorkflow to Temporal
│   ├── workflows/
│   │   ├── deploy.go             # InfraDeployWorkflow — top-level orchestrator
│   │   ├── vpc.go                # VpcWorkflow — full VPC build (VPC + IGW + subnets + NAT + routes)
│   │   ├── natgateway.go         # NatGatewayWorkflow — EIP + NAT Gateway pair
│   │   ├── routetable.go         # RouteTableWorkflow — create + routes + subnet associations
│   │   ├── sg.go                 # SecurityGroupWorkflow — security group + rules
│   │   ├── tgw.go                # TgwWorkflow — Transit Gateway + VPC attachments + routes
│   │   └── vpn.go                # VpnWorkflow — Client VPN endpoint
│   └── activities/
│       ├── common.go             # InfraActivities struct, upStack(), heartbeatWriter, helpers
│       ├── vpc.go                # VPC/subnet/IGW/NAT/route table activity types + implementations
│       ├── sg.go                 # Security group activity types + implementations
│       ├── tgw.go                # Transit Gateway activity types + implementations
│       └── vpn.go                # Client VPN activity types + implementations
│
├── argocd/
│   ├── root.yaml                 # Root ArgoCD Application (app-of-apps entry point)
│   ├── apps/                     # ArgoCD Application definitions
│   │   ├── argocd.yaml           # ArgoCD self-manages itself
│   │   ├── cert-manager.yaml
│   │   ├── envoy-gateway.yaml
│   │   ├── sealed-secrets.yaml
│   │   ├── apps-auto-dev.yaml    # Auto-sync dev app deployments
│   │   ├── apps-auto-qa.yaml     # Auto-sync QA app deployments
│   │   ├── apps-prod.yaml        # Manual-sync prod app deployments
│   │   └── team-a-example-app-preview.yaml  # PR preview environments via PR Generator
│   └── install/                  # ArgoCD installation config (Kustomize patches)
│       ├── kustomization.yaml
│       ├── argocd-cm-patch.yaml
│       ├── argocd-params-patch.yaml
│       ├── certificate.yaml
│       ├── httproute.yaml
│       └── namespace.yaml
│
├── infrastructure/               # Kubernetes infrastructure add-on configs
│   ├── cert-manager/
│   │   ├── kustomization.yaml
│   │   └── cluster-issuer.yaml
│   ├── envoy-gateway/
│   │   └── kustomization.yaml
│   └── sealed-secrets/
│       └── kustomization.yaml
│
├── apps/                         # Application manifests (Kustomize base + overlays)
│   └── team-a/
│       └── example-app/
│           ├── base/             # Deployment + Service
│           └── overlays/         # dev / qa / prod patches
│
└── README.md
```

## Entry Points

| Binary | Path | Purpose |
|---|---|---|
| Worker | `temporal/infra-worker/worker/main.go` | Long-running Temporal worker; registers all workflows and activities; must be running for any provisioning to execute |
| Starter | `temporal/infra-worker/starter/main.go` | One-shot CLI to submit `InfraDeployWorkflow`; reads `SERVER_CERT_ARN` and `CLIENT_CA_ARN` from env |

## Package Organization

The Go module is organized by **layer**, not by domain:

- `workflows/` — all Temporal workflow definitions; no AWS SDK calls, no I/O, deterministic only
- `activities/` — all Pulumi provisioning logic; one file per AWS service domain
- `worker/` + `starter/` — thin `main` packages for process entry

All activity implementations live on the `InfraActivities` struct (`activities/common.go`), which carries `ProjectName` and `Region` as shared config. This allows all activity methods to be registered in a single `w.RegisterActivity(acts)` call.

## Workflow Registration

`worker/main.go` registers all 7 workflows and the single activity struct:

```go
w.RegisterActivity(acts)                          // all activities via method set
w.RegisterWorkflow(workflows.InfraDeployWorkflow)
w.RegisterWorkflow(workflows.VpcWorkflow)
w.RegisterWorkflow(workflows.NatGatewayWorkflow)
w.RegisterWorkflow(workflows.RouteTableWorkflow)
w.RegisterWorkflow(workflows.SecurityGroupWorkflow)
w.RegisterWorkflow(workflows.TgwWorkflow)
w.RegisterWorkflow(workflows.VpnWorkflow)
```

## Naming Conventions for Pulumi Stacks

Stack names are constructed from the workflow input's `StackName` field with suffixes for sub-resources. Pattern: `<stackName>-<resource>[-<index>]`

Examples from `VpcWorkflow`:
- VPC: `"ops-vpc"`
- IGW: `"ops-vpc-igw"`
- Public subnet 0: `"ops-vpc-public-subnet-0"`
- Private subnet 2: `"ops-vpc-private-subnet-2"`
- NAT 0: `"ops-vpc-nat-0"`
- Private route table 1: `"ops-vpc-private-rt-1"`
