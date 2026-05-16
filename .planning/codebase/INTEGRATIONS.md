# External Integrations

**Analysis Date:** 2026-05-16

## APIs & External Services

**Temporal Workflow Orchestration:**
- Service: Temporal Server (self-hosted or Temporal Cloud)
- SDK: `go.temporal.io/sdk` v1.43.0
- Transport: gRPC (`google.golang.org/grpc` v1.80.0)
- Auth: None configured in code — unauthenticated `client.Dial` to `TEMPORAL_HOST_PORT`
- Task queue: `infra-deploy` (hardcoded in `temporal/infra-worker/worker/main.go`)
- Registered workflows: `InfraDeployWorkflow`, `VpcWorkflow`, `NatGatewayWorkflow`, `RouteTableWorkflow`, `SecurityGroupWorkflow`, `TgwWorkflow`, `VpnWorkflow`
- Query handler: `status` — returns current phase and per-VPC readiness

**AWS (via Pulumi Automation API):**
- Auth: Standard AWS SDK credential chain (env vars, instance profile, shared credentials file) — no SDK client constructed directly; credentials are consumed by the Pulumi AWS provider at stack execution time
- Region env var: `AWS_REGION` (default: `us-east-1`); set per-stack via `stack.SetConfig(ctx, "aws:region", ...)`
- Provider package: `github.com/pulumi/pulumi-aws/sdk/v6` v6.83.3
- Plugin installed at worker startup: `aws@v6.83.3` via `workspace.InstallPlugin` (`temporal/infra-worker/worker/main.go`)

## AWS Services Used

All AWS resource provisioning happens through Pulumi inline programs executed inside Temporal activities. Each resource type maps to a Pulumi resource in `temporal/infra-worker/activities/`:

| AWS Service | Pulumi Package | Activity File | Resources Created |
|---|---|---|---|
| EC2 VPC | `ec2` | `activities/vpc.go` | VPC, Internet Gateway, Subnet, EIP, NAT Gateway, Route Table, Route, RouteTableAssociation |
| EC2 Transit Gateway | `ec2transitgateway` | `activities/tgw.go` | TransitGateway, VpcAttachment |
| EC2 Client VPN | `ec2clientvpn` | `activities/vpn.go` | Endpoint, NetworkAssociation, AuthorizationRule, Route |
| EC2 Security Groups | `ec2` | `activities/sg.go` | SecurityGroup, SecurityGroupRule |

**ACM (AWS Certificate Manager):**
- Not provisioned by this repo — ARNs consumed as inputs
- `SERVER_CERT_ARN`: ACM ARN for the Client VPN server TLS certificate
- `CLIENT_CA_ARN`: ACM ARN of the mutual-TLS client CA certificate
- Both passed into `VpnWorkflow` → `VpnInput` struct (`activities/vpn.go`)

## Pulumi Automation API

- SDK: `github.com/pulumi/pulumi/sdk/v3` v3.239.0
- Mode: **Inline programs only** — no `Pulumi.yaml` project files, no CLI invocations
- Stack management: `auto.UpsertStackInlineSource` creates or opens a local stack for every activity call
- State backend: Local filesystem (default for `auto.NewLocalWorkspace`); no remote backend configured in code
- Project name: `"gitops-infra"` (hardcoded in `InfraActivities.ProjectName`, `temporal/infra-worker/worker/main.go`)
- Heartbeat integration: `heartbeatWriter` streams Pulumi progress output to Temporal heartbeats so the server can detect stuck activities (`activities/common.go`)

## GitOps / Kubernetes

**ArgoCD:**
- Version: v3.4.2 (HA install from `https://raw.githubusercontent.com/argoproj/argo-cd/v3.4.2/manifests/ha/install.yaml`)
- Mode: Self-managed — ArgoCD Application `argocd` points back to `argocd/install/` in this repo
- App of Apps root: `argocd/root.yaml` watches `argocd/apps/` and auto-registers all Application/ApplicationSet manifests
- Source repo: `https://github.com/Amertz08/gitops-infra`

**ApplicationSets in `argocd/apps/`:**

| Name | Generator | Target Cluster | Sync |
|---|---|---|---|
| `apps-auto-qa` | git directories (`apps/*/*/overlays/qa`) | `qa-cluster` | Automated |
| `apps-auto-dev` | git directories (`apps/*/*/overlays/dev`) | `dev-cluster` | Automated |
| `apps-prod` | (see file) | prod cluster | Manual sync required |
| `team-a-example-app-preview` | pullRequest (GitHub PR generator) | `dev-cluster` | Automated per PR |
| `sealed-secrets` | clusters | all clusters | Automated |
| `envoy-gateway` | clusters | all clusters | Automated |

**GitHub (Pull Request generator):**
- Integration: ArgoCD ApplicationSet `team-a-example-app-preview` polls GitHub PRs
- Source org/repo: `Amertz08/example-app`
- Auth: Kubernetes Secret `github-token` (key: `token`) in `argocd` namespace — referenced in `argocd/apps/team-a-example-app-preview.yaml`
- Poll interval: 180 seconds (`requeueAfterSeconds: 180`)

## Data Storage

**Databases:**
- None — this repo manages network infrastructure, not application data

**File Storage:**
- Pulumi stack state: local filesystem (default `auto.NewLocalWorkspace` backend); no S3 or Pulumi Cloud backend configured in code

**Caching:**
- None

## Authentication & Identity

**Temporal Client:**
- Unauthenticated `client.Dial` — suitable for local/private Temporal deployments
- Location: `temporal/infra-worker/worker/main.go`, `temporal/infra-worker/starter/main.go`

**AWS:**
- Standard AWS SDK credential chain; no explicit credentials in code
- IAM permissions required: EC2 full, TransitGateway, ClientVPN, ACM read

**ArgoCD TLS:**
- cert-manager ClusterIssuer `selfsigned` issues a self-signed certificate for `argocd.local`
- Certificate resource: `argocd/install/certificate.yaml`; stores TLS secret `argocd-tls`

**Sealed Secrets:**
- Bitnami Sealed Secrets v0.27.3 — cluster-side controller decrypts `SealedSecret` resources
- GitHub token for ArgoCD PR generator is expected to be stored as a SealedSecret in the `argocd` namespace

## Networking / Ingress

**Envoy Gateway v1.2.0:**
- Kubernetes Gateway API implementation (`gatewayClassName: eg`)
- ArgoCD is exposed via a `Gateway` + `HTTPRoute` defined in `argocd/install/httproute.yaml`
- HTTP→HTTPS redirect (301) configured; HTTPS terminates at the Gateway using the `argocd-tls` secret

## CI/CD & Deployment

**Hosting:**
- Kubernetes (cluster details not specified in this repo — targets `https://kubernetes.default.svc` for in-cluster ArgoCD)

**CI Pipeline:**
- Not present in this repo — app source repos are expected to build/push images and update `newTag` in overlay `kustomization.yaml` files

## Environment Configuration

**Required env vars (Temporal worker):**
- `TEMPORAL_HOST_PORT` — Temporal server gRPC address (default: `localhost:7233`)
- `AWS_REGION` — AWS region (default: `us-east-1`)
- `SERVER_CERT_ARN` — ACM ARN for VPN server cert (starter only)
- `CLIENT_CA_ARN` — ACM ARN for VPN client CA cert (starter only)
- Standard AWS credential env vars (`AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`) or equivalent

**Required Kubernetes Secrets:**
- `argocd/github-token` (key: `token`) — GitHub personal access token for PR preview ApplicationSet

## Webhooks & Callbacks

**Incoming:**
- None — ArgoCD polls GitHub; no webhook endpoints are defined in this repo

**Outgoing:**
- None explicitly configured

---

*Integration audit: 2026-05-16*
