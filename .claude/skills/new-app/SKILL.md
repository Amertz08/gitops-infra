---
name: new-app
description: Scaffold a new application in this GitOps repo. Creates base manifests and dev/qa/prod Kustomize overlays under apps/<team>/<app>/.
metadata:
  domain: gitops
  triggers: new app, scaffold app, create app, new application, onboard app
---

# New Application Scaffold

## Purpose
Create a new Kustomize-based application following this repo's team/app/base+overlays convention. Generates all required files so the application is immediately picked up by the ArgoCD ApplicationSets and deployed to dev and qa on push.

## When to Use This Skill
- A team wants to onboard a new service or application
- You need to create the correct directory structure for a new app

## Workflow

### Step 1 — Collect inputs

Ask the user for the following with AskUserQuestion:

- **Team name** — lowercase, hyphens only (e.g. `payments`, `team-a`). This becomes the directory name under `apps/` and the namespace prefix.
- **App name** — lowercase, hyphens only (e.g. `api-gateway`, `worker`).
- **Container image name** — image name without a tag (e.g. `nginx`, `ghcr.io/org/my-app`). The tag is managed per environment.
- **Initial image tag** — the tag to use across all three environments to start (e.g. `latest`, `main`, a git SHA like `abc1234`). Teams promote by updating `newTag` in each overlay via PR.
- **Container port** — the port the container listens on (e.g. `8080`). **Optional** — if not provided, no Service is created and no ports are defined on the container.
- **GitHub org** — the GitHub org or user that owns the app source repo (e.g. `my-org`). Used for PR preview environments.
- **GitHub repo** — the app source repo name (e.g. `my-app`). Used for PR preview environments.

Validate: team name and app name must match `^[a-z0-9][a-z0-9-]*$`. If they contain uppercase or spaces, tell the user and ask again.

### Step 2 — Create files

Create all files under `apps/<TEAM>/<APP>/`. Use the templates below, substituting `<TEAM>`, `<APP>`, `<IMAGE>` (image name, no tag), `<TAG>`, and `<PORT>`.

#### `apps/<TEAM>/<APP>/base/kustomization.yaml`

If port provided:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
  - service.yaml
```

If no port:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - deployment.yaml
```

#### `apps/<TEAM>/<APP>/base/deployment.yaml`

The image here has **no tag** — the tag is set per environment in the overlay `images:` stanza.

If port provided:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: <APP>
spec:
  replicas: 1
  selector:
    matchLabels:
      app: <APP>
  template:
    metadata:
      labels:
        app: <APP>
    spec:
      containers:
        - name: <APP>
          image: <IMAGE>
          ports:
            - containerPort: <PORT>
```

If no port:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: <APP>
spec:
  replicas: 1
  selector:
    matchLabels:
      app: <APP>
  template:
    metadata:
      labels:
        app: <APP>
    spec:
      containers:
        - name: <APP>
          image: <IMAGE>
```

#### `apps/<TEAM>/<APP>/base/service.yaml` — only create if port was provided

```yaml
apiVersion: v1
kind: Service
metadata:
  name: <APP>
spec:
  selector:
    app: <APP>
  ports:
    - port: <PORT>
      targetPort: <PORT>
```

#### `apps/<TEAM>/<APP>/overlays/dev/kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: <TEAM>-dev

resources:
  - ../../base

images:
  - name: <IMAGE>
    newTag: "<TAG>"
```

#### `apps/<TEAM>/<APP>/overlays/qa/kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: <TEAM>-qa

resources:
  - ../../base

images:
  - name: <IMAGE>
    newTag: "<TAG>"
```

#### `apps/<TEAM>/<APP>/overlays/prod/kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: <TEAM>-prod

resources:
  - ../../base

images:
  - name: <IMAGE>
    newTag: "<TAG>"

patches:
  - patch: |-
      - op: replace
        path: /spec/replicas
        value: 3
    target:
      kind: Deployment
      name: <APP>
```

### Step 4 — Create preview ApplicationSet

Always create `argocd/apps/<TEAM>-<APP>-preview.yaml` (no prompt — this is always generated):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: <TEAM>-<APP>-preview
  namespace: argocd
spec:
  generators:
    - pullRequest:
        github:
          owner: <GITHUB_ORG>
          repo: <GITHUB_REPO>
          tokenRef:
            secretName: github-token
            key: token
        requeueAfterSeconds: 180
  template:
    metadata:
      name: '<TEAM>-<APP>-pr-{{number}}'
    spec:
      project: default
      source:
        repoURL: https://github.com/Amertz08/gitops-infra
        targetRevision: HEAD
        path: apps/<TEAM>/<APP>/overlays/dev
        kustomize:
          images:
            - '<IMAGE>:{{head_sha}}'
      destination:
        name: dev-cluster
        namespace: '<TEAM>-<APP>-pr-{{number}}'
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
        syncOptions:
          - CreateNamespace=true
          - ServerSideApply=true
```

### Step 5 — Summarize and offer to commit

After writing the files, tell the user:
- Files created at `apps/<TEAM>/<APP>/`
- Preview ApplicationSet created at `argocd/apps/<TEAM>-<APP>-preview.yaml`
- **dev + qa** will auto-sync once pushed (ArgoCD ApplicationSet picks them up within ~3 minutes)
- **prod** requires a manual sync in the ArgoCD UI after dev/qa validation
- **PR previews** deploy automatically when a PR is opened in `<GITHUB_ORG>/<GITHUB_REPO>` — the preview namespace `<TEAM>-<APP>-pr-<number>` is created automatically and deleted when the PR is closed
- Namespaces that will be created: `<TEAM>-dev`, `<TEAM>-qa`, `<TEAM>-prod`, `<TEAM>-<APP>-pr-<number>` (one per open PR)
- **Prerequisite:** a `github-token` Secret must exist in the `argocd` namespace. See the [PR Preview section in the README](../../README.md) for setup instructions.
- To deploy a new image version: open a PR updating `newTag` in `overlays/dev/kustomization.yaml`, merge to auto-deploy to dev, then repeat for qa, then prod (prod also requires a manual ArgoCD sync)

Then ask if they want to commit and push the new files now.

If yes, run:
```bash
git add apps/<TEAM>/<APP>/ argocd/apps/<TEAM>-<APP>-preview.yaml
git commit -m "Add <TEAM>/<APP> application scaffold"
git push
```
