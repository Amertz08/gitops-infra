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

### Step 3 — Summarize and offer to commit

After writing the files, tell the user:
- Files created at `apps/<TEAM>/<APP>/`
- **dev + qa** will auto-sync once pushed (ArgoCD ApplicationSet picks them up within ~3 minutes)
- **prod** requires a manual sync in the ArgoCD UI after dev/qa validation
- Namespaces that will be created: `<TEAM>-dev`, `<TEAM>-qa`, `<TEAM>-prod`
- To deploy a new image version: open a PR updating `newTag` in `overlays/dev/kustomization.yaml`, merge to auto-deploy to dev, then repeat for qa, then prod (prod also requires a manual ArgoCD sync)

Then ask if they want to commit and push the new files now.

If yes, run:
```bash
git add apps/<TEAM>/<APP>/
git commit -m "Add <TEAM>/<APP> application scaffold"
git push
```
