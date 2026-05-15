# gitops-infra

GitOps infrastructure repository. ArgoCD watches this repo and reconciles all changes to the cluster automatically.

## What's in this repo

| Directory | Purpose |
|---|---|
| `argocd/install/` | ArgoCD installation (Kustomize overlay, self-managed) |
| `argocd/apps/` | ArgoCD Application and ApplicationSet manifests |
| `argocd/root.yaml` | App of Apps root — auto-registers everything in `argocd/apps/` |
| `infrastructure/` | Cluster infrastructure managed by ArgoCD |
| `apps/` | Team application workloads |
| `.claude/skills/` | Claude Code skills for this repo |

### Infrastructure components

- **ArgoCD v3.4.2** — GitOps controller, self-manages from `argocd/install/`
- **Envoy Gateway v1.2.0** — Kubernetes Gateway API implementation
- **Sealed Secrets** — Encrypt secrets with a cluster key so they can be committed to Git

## Repository structure for teams

Applications follow a Kustomize base + overlays pattern:

```
apps/
└── <team>/
    └── <app>/
        ├── base/                  # Shared manifests (all environments)
        │   ├── deployment.yaml
        │   ├── service.yaml       # Omit if the app has no port
        │   └── kustomization.yaml
        └── overlays/
            ├── dev/               # Auto-synced on push
            ├── qa/                # Auto-synced on push
            └── prod/              # Manual sync required
```

**Namespaces** are created automatically: `<team>-dev`, `<team>-qa`, `<team>-prod`.

**ArgoCD Applications** are generated automatically by the ApplicationSets in `argocd/apps/`. No ArgoCD YAML is required per app — just push the directory structure.

## Adding a new application

### Option 1 — Claude Code skill (recommended)

Open this repo in [Claude Code](https://claude.ai/code) and run:

```
/new-app
```

The skill will prompt you for team name, app name, container image, and (optionally) a port, then generate all required files and offer to commit.

**Installing the skill:** The skill is bundled in `.claude/skills/new-app/SKILL.md` and is automatically available when you open this repo in Claude Code — no installation required.

### Option 2 — Copy the template manually

```bash
cp -r apps/team-a/example-app apps/<your-team>/<your-app>
```

Then update:
- Namespaces in each overlay (`overlays/*/kustomization.yaml`) from `team-a-*` to `<your-team>-*`
- The image in `base/deployment.yaml`
- Remove `base/service.yaml` and its entry in `base/kustomization.yaml` if your app has no port

Commit and push. ArgoCD will deploy to dev and qa automatically within ~3 minutes.

## Secrets

Use [Sealed Secrets](https://github.com/bitnami-labs/sealed-secrets) to store secrets in Git. The controller's public key is in the cluster — never commit the private key.

```bash
# Encrypt a secret
kubectl create secret generic my-secret \
  --dry-run=client \
  --from-literal=password=... \
  -n <team>-<env> \
  -o yaml \
  | kubeseal --format yaml > apps/<team>/<app>/overlays/<env>/sealed-secret.yaml
```

Commit the `SealedSecret` YAML. ArgoCD will deploy it; the controller decrypts it at runtime.

Back up the controller key — if the cluster is recreated without it, sealed secrets cannot be decrypted:

```bash
kubectl -n kube-system get secret \
  -l sealedsecrets.bitnami.com/sealed-secrets-key=active \
  -o yaml > sealed-secrets-key-backup.yaml
# Store outside the cluster and outside this repo
```

## Bootstrap (initial cluster setup)

These steps are only needed when setting up a brand new cluster. Everything after step 4 is self-managed.

```bash
# 1. Install ArgoCD
kubectl apply --server-side -k argocd/install/

# 2. Wait for ArgoCD
kubectl -n argocd rollout status deployment/argocd-server

# 3. Get the initial admin password
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath="{.data.password}" | base64 -d && echo

# 4. Register the self-managing Application
kubectl apply -f argocd/apps/argocd.yaml

# 5. Bootstrap Envoy Gateway
kubectl apply --server-side \
  -f https://github.com/envoyproxy/gateway/releases/download/v1.2.0/install.yaml

# 6. Register the App of Apps root (auto-registers everything else)
kubectl apply -f argocd/root.yaml
```

After step 6, all ApplicationSets and Applications in `argocd/apps/` are managed by ArgoCD.

## Accessing the ArgoCD UI (local cluster)

The Gateway API route is configured for `argocd.local` but requires a cloud LoadBalancer to be reachable. On a local cluster, use port-forward instead:

```bash
kubectl -n argocd port-forward svc/argocd-server 8080:80
# Open http://localhost:8080
```
