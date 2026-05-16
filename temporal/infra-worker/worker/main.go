package main

import (
	"log"
	"os"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"github.com/adammertz/gitops-infra/temporal/infra-worker/workflows"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	hostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
	}

	c, err := client.Dial(client.Options{HostPort: hostPort})
	if err != nil {
		log.Fatalln("Unable to create Temporal client:", err)
	}
	defer c.Close()

	w := worker.New(c, "infra-deploy", worker.Options{})

	repoRoot := os.Getenv("REPO_ROOT")
	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalln("Cannot determine repo root:", err)
		}
		repoRoot = cwd
	}

	acts := &activities.PulumiActivities{RepoRoot: repoRoot}
	w.RegisterActivity(acts)
	w.RegisterWorkflow(workflows.InfraDeployWorkflow)

	log.Printf("Worker started (task queue: infra-deploy, repo root: %s)", repoRoot)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("Worker stopped:", err)
	}
}
