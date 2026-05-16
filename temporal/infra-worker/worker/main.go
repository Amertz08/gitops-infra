package main

import (
	"context"
	"log"
	"os"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"github.com/adammertz/gitops-infra/temporal/infra-worker/workflows"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	// Install the Pulumi AWS provider plugin before starting the worker so that
	// inline programs can run without downloading the plugin mid-activity.
	if err := installPulumiPlugins(); err != nil {
		log.Printf("Warning: Pulumi plugin install failed (may already be installed): %v", err)
	}

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

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	acts := &activities.InfraActivities{
		ProjectName: "gitops-infra",
		Region:      region,
	}
	w.RegisterActivity(acts)
	w.RegisterWorkflow(workflows.InfraDeployWorkflow)
	w.RegisterWorkflow(workflows.VpcWorkflow)
	w.RegisterWorkflow(workflows.TgwWorkflow)
	w.RegisterWorkflow(workflows.VpnWorkflow)

	log.Printf("Worker started (task queue: infra-deploy, region: %s)", region)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalln("Worker stopped:", err)
	}
}

func installPulumiPlugins() error {
	ws, err := auto.NewLocalWorkspace(context.Background())
	if err != nil {
		return err
	}
	return ws.InstallPlugin(context.Background(), "aws", activities.AWSPluginVersion)
}
