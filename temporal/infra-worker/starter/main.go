package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"github.com/adammertz/gitops-infra/temporal/infra-worker/workflows"
	"go.temporal.io/sdk/client"
)

func main() {
	project := flag.String("project", "aws-networking", "Pulumi project directory under pulumi/")
	stackName := flag.String("stack", "main", "Pulumi stack name")
	flag.Parse()

	hostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
	}

	c, err := client.Dial(client.Options{HostPort: hostPort})
	if err != nil {
		log.Fatalln("Unable to create Temporal client:", err)
	}
	defer c.Close()

	input := activities.StackInput{
		Project:   *project,
		StackName: *stackName,
	}

	workflowID := fmt.Sprintf("infra-deploy-%s-%s", *project, *stackName)
	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: "infra-deploy",
	}, workflows.InfraDeployWorkflow, input)
	if err != nil {
		log.Fatalln("Unable to start workflow:", err)
	}

	fmt.Printf("Started workflow: %s (run: %s)\n", we.GetID(), we.GetRunID())
	fmt.Printf("  status:  temporal workflow query --workflow-id %s --query-type status\n", we.GetID())
	fmt.Printf("  ui:      http://localhost:8233/namespaces/default/workflows/%s\n\n", we.GetID())

	var result activities.StackOutputs
	if err := we.Get(context.Background(), &result); err != nil {
		log.Fatalln("Deployment failed:", err)
	}

	fmt.Println("Deployment succeeded. Stack outputs:")
	for k, v := range result.Outputs {
		fmt.Printf("  %-30s = %s\n", k, v)
	}
}
