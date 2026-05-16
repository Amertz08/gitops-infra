# Technology Stack

**Analysis Date:** 2026-05-16

## Languages

**Primary:**
- Go 1.25.8 — Temporal worker, workflow definitions, activity implementations
  - Module root: `temporal/infra-worker/go.mod`

**Secondary:**
- YAML — Kubernetes manifests, Kustomize overlays, ArgoCD Applications/ApplicationSets
  - Locations: `argocd/`, `infrastructure/`, `apps/`

## Runtime

**Environment:**
- Go binary runtime — the Temporal worker is a compiled Go binary
- Kubernetes — all GitOps-managed workloads run on Kubernetes clusters

**Package Manager:**
- Go modules (`go mod`)
- Lockfile: `temporal/infra-worker/go.sum` (present, committed)

## Frameworks

**Workflow Orchestration:**
- Temporal Go SDK `go.temporal.io/sdk` v1.43.0 — workflow and activity runtime
  - API types: `go.temporal.io/api` v1.62.11
  - Nexus integration: `github.com/nexus-rpc/sdk-go` v0.6.0

**Infrastructure as Code:**
- Pulumi Automation API `github.com/pulumi/pulumi/sdk/v3` v3.239.0 — inline program execution without a Pulumi CLI invocation; all stacks use `auto.UpsertStackInlineSource`
- Pulumi AWS Provider `github.com/pulumi/pulumi-aws/sdk/v6` v6.83.3 — AWS resource types for EC2, EC2 Transit Gateway, EC2 Client VPN

**GitOps / CD:**
- ArgoCD v3.4.2 — GitOps controller, self-managed from `argocd/install/`
- Kustomize — manifest composition (base + overlay pattern); no Helm charts present

**Networking / Ingress:**
- Envoy Gateway v1.2.0 (`infrastructure/envoy-gateway/kustomization.yaml`) — Kubernetes Gateway API implementation
- Kubernetes Gateway API (`gateway.networking.k8s.io/v1`) — HTTPRoute, Gateway resources in `argocd/install/httproute.yaml`

**Secret Management:**
- Sealed Secrets v0.27.3 (bitnami-labs) — controller deployed via `infrastructure/sealed-secrets/kustomization.yaml`

**TLS / Certificate Management:**
- cert-manager — ClusterIssuer and Certificate resources in `infrastructure/cert-manager/`; currently configured with a self-signed issuer (`selfsigned`)

**Testing:**
- `github.com/stretchr/testify` v1.11.1 — listed as indirect dependency; no test files found in the current codebase

**Build/Dev:**
- No Makefile or Dockerfile detected at repo root
- Pulumi AWS plugin version pinned as a constant (`AWSPluginVersion = "v6.83.3"` in `temporal/infra-worker/activities/common.go`) and installed at worker startup via `auto.NewLocalWorkspace` + `InstallPlugin`

## Key Dependencies

**Critical:**
- `go.temporal.io/sdk` v1.43.0 — entire workflow orchestration layer; worker, client, heartbeat, retry policies
- `github.com/pulumi/pulumi/sdk/v3` v3.239.0 — Automation API; `auto.UpsertStackInlineSource`, `stack.Up`, config management
- `github.com/pulumi/pulumi-aws/sdk/v6` v6.83.3 — AWS resource definitions: `ec2`, `ec2transitgateway`, `ec2clientvpn` packages

**Infrastructure (transitive, notable):**
- `google.golang.org/grpc` v1.80.0 — gRPC transport for Temporal client↔server communication
- `go.opentelemetry.io/otel` v1.43.0 — OpenTelemetry tracing (pulled in by Temporal SDK)
- `github.com/go-git/go-git/v5` v5.19.0 — pulled in by Pulumi SDK (used internally for stack source management)
- `github.com/pulumi/esc` v0.17.0 — Pulumi ESC (Environment, Secrets, Configuration) client

## Configuration

**Environment:**
- `TEMPORAL_HOST_PORT` — Temporal server address; defaults to `client.DefaultHostPort` (`localhost:7233`)
- `AWS_REGION` — AWS region for all Pulumi stacks; defaults to `us-east-1`
- `SERVER_CERT_ARN` — ACM ARN for the Client VPN server certificate (read in `temporal/infra-worker/starter/main.go`)
- `CLIENT_CA_ARN` — ACM ARN for the Client VPN CA certificate (read in `temporal/infra-worker/starter/main.go`)
- AWS credentials: must be available via the standard AWS SDK credential chain (env vars, instance profile, etc.) — no explicit credential file references in code

**Build:**
- No dedicated build config file; standard `go build ./...` from `temporal/infra-worker/`

## Platform Requirements

**Development:**
- Go 1.25.8+
- Pulumi CLI (optional — the worker uses the Automation API exclusively, not the CLI)
- AWS credentials with EC2, Transit Gateway, and Client VPN permissions
- A running Temporal server (local: `temporal server start-dev` or Docker)

**Production:**
- Kubernetes cluster (ArgoCD target)
- Temporal server cluster accessible at `TEMPORAL_HOST_PORT`
- AWS account with appropriate IAM permissions
- ACM certificates for Client VPN (server cert + CA cert)

---

*Stack analysis: 2026-05-16*
