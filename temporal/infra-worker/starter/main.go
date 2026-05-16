package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/workflows"
	"go.temporal.io/sdk/client"
)

func main() {
	region := flag.String("region", "us-east-1", "AWS region")
	flag.Parse()

	serverCertArn := os.Getenv("SERVER_CERT_ARN")
	clientCaArn := os.Getenv("CLIENT_CA_ARN")

	hostPort := os.Getenv("TEMPORAL_HOST_PORT")
	if hostPort == "" {
		hostPort = client.DefaultHostPort
	}

	c, err := client.Dial(client.Options{HostPort: hostPort})
	if err != nil {
		log.Fatalln("Unable to create Temporal client:", err)
	}
	defer c.Close()

	input := workflows.InfraInput{
		Region:        *region,
		ServerCertArn: serverCertArn,
		ClientCaArn:   clientCaArn,
	}

	we, err := c.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        "infra-deploy-main",
		TaskQueue: "infra-deploy",
	}, workflows.InfraDeployWorkflow, input)
	if err != nil {
		log.Fatalln("Unable to start workflow:", err)
	}

	fmt.Printf("Started workflow: %s (run: %s)\n", we.GetID(), we.GetRunID())
	fmt.Printf("  status:  temporal workflow query --workflow-id %s --query-type status\n", we.GetID())
	fmt.Printf("  ui:      http://localhost:8233/namespaces/default/workflows/%s\n\n", we.GetID())

	var result workflows.InfraOutputs
	if err := we.Get(context.Background(), &result); err != nil {
		log.Fatalln("Deployment failed:", err)
	}

	fmt.Println("Deployment succeeded.")
	fmt.Printf("  ops VPC:  %s\n", result.OpsVpc.VpcId)
	fmt.Printf("  qa VPC:   %s\n", result.QaVpc.VpcId)
	fmt.Printf("  prod VPC: %s\n", result.ProdVpc.VpcId)
	fmt.Printf("  TGW:      %s\n", result.Tgw.TgwId)
	fmt.Printf("  VPN:      %s\n", result.Vpn.EndpointId)
}
