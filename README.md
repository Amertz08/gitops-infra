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
- The image name in `base/deployment.yaml` (no tag — tag goes in overlays)
- The `images[0].name` and `images[0].newTag` in each overlay
- Remove `base/service.yaml` and its entry in `base/kustomization.yaml` if your app has no port

Commit and push. ArgoCD will deploy to dev and qa automatically within ~3 minutes.

## Promoting an image

Image tags live in each overlay's `kustomization.yaml` under `images:`, not in `base/deployment.yaml`. This lets environments run independently — dev can have a new build while prod stays on a validated version.

```yaml
# apps/<team>/<app>/overlays/dev/kustomization.yaml
images:
  - name: ghcr.io/org/my-app
    newTag: abc1234   # ← update this to promote
```

**Promotion workflow:**

1. Build and push a new image from your app repo (`ghcr.io/org/my-app:abc1234`)
2. Open a PR updating `newTag` in `overlays/dev/kustomization.yaml` → merge → ArgoCD auto-deploys to dev
3. After validating in dev, open a PR updating `newTag` in `overlays/qa/kustomization.yaml` → merge → ArgoCD auto-deploys to qa
4. After validating in qa, open a PR updating `newTag` in `overlays/prod/kustomization.yaml` → merge → trigger a manual sync in the ArgoCD UI to deploy to prod

## PR preview environments

Every PR opened in an app's source repo gets its own isolated preview namespace deployed automatically. ArgoCD's ApplicationSet Pull Request generator polls GitHub for open PRs, creates a namespaced Application per PR, and deletes it when the PR is closed.

### How it works

1. Developer opens PR in the app source repo
2. CI builds and pushes an image tagged with the PR's **head commit SHA**: `ghcr.io/org/my-app:<sha>`
3. ArgoCD detects the PR (polls every 3 min), creates Application `<team>-<app>-pr-<number>`
4. Application deploys to namespace `<team>-<app>-pr-<number>` using the dev overlay, with the image tag overridden to `<sha>`
5. PR is merged or closed → ArgoCD deletes the Application and namespace automatically

### One-time cluster setup — `github-token` Secret

The PR generator authenticates with GitHub using a Personal Access Token stored as a SealedSecret in the `argocd` namespace. Create it once per cluster:

1. Generate a GitHub PAT with **`repo` scope** (private repos) or **`public_repo` scope** (public repos)
2. Encrypt and commit it:

```bash
kubectl create secret generic github-token \
  --dry-run=client \
  --from-literal=token=<YOUR_GITHUB_PAT> \
  -n argocd \
  -o yaml \
  | kubeseal --format yaml > argocd/install/github-token-sealed.yaml
```

3. Add `github-token-sealed.yaml` to `argocd/install/kustomization.yaml` under `resources:`
4. Commit and push — ArgoCD will deploy the SealedSecret and the controller will decrypt it

### App repo CI contract

Your app repo's CI pipeline must build and push an image tagged with the **full head commit SHA** when a PR is opened or updated:

```yaml
# Example GitHub Actions step
- name: Build and push PR image
  run: |
    docker build -t ghcr.io/org/my-app:${{ github.event.pull_request.head.sha }} .
    docker push ghcr.io/org/my-app:${{ github.event.pull_request.head.sha }}
```

The preview ApplicationSet uses `{{head_sha}}` to reference this exact tag.

### Enabling previews for a new app

Run `/new-app` in Claude Code — preview ApplicationSets are generated automatically as part of every scaffold. The ApplicationSet file is created at `argocd/apps/<team>-<app>-preview.yaml` and committed alongside the app manifests.

To add previews manually to an existing app, copy `argocd/apps/team-a-example-app-preview.yaml`, update `github.owner`, `github.repo`, the `path:`, `name:`, `namespace:`, and `images:` fields, and commit.

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
