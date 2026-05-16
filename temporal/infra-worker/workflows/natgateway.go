package workflows

import (
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// NatGatewayWorkflow creates an Elastic IP then provisions a NAT Gateway in the given subnet:
//
//	Step 1: CreateEip
//	Step 2: CreateNatGateway  (needs EIP allocation ID)
func NatGatewayWorkflow(ctx workflow.Context, input activities.NatGatewayInput) (activities.NatGatewayOutput, error) {
	var acts *activities.InfraActivities

	shortCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	// NAT Gateway provisioning can take ~5 min in AWS.
	longCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	var eipOut activities.CreateEipOutput
	if err := workflow.ExecuteActivity(shortCtx, acts.CreateEip, activities.CreateEipInput{
		StackName:   input.StackName + "-eip",
		Environment: input.Environment,
		ExtraTags:   input.ExtraTags,
	}).Get(ctx, &eipOut); err != nil {
		return activities.NatGatewayOutput{}, err
	}

	var natOut activities.CreateNatGatewayOutput
	if err := workflow.ExecuteActivity(longCtx, acts.CreateNatGateway, activities.CreateNatGatewayInput{
		StackName:   input.StackName + "-nat",
		Environment: input.Environment,
		SubnetId:    input.SubnetId,
		EipId:       eipOut.EipId,
		ExtraTags:   input.ExtraTags,
	}).Get(ctx, &natOut); err != nil {
		return activities.NatGatewayOutput{}, err
	}

	return activities.NatGatewayOutput{NatGwId: natOut.NatGwId}, nil
}
