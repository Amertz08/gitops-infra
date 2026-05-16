package workflows

import (
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RouteTableWorkflow creates a route table, adds routes, and associates subnets:
//
//	Step 1: CreateRouteTable
//	Step 2: AddRoutes | AssociateSubnets (parallel)
func RouteTableWorkflow(ctx workflow.Context, input activities.RouteTableInput) (activities.RouteTableOutput, error) {
	var acts *activities.InfraActivities

	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	var rtOut activities.CreateRouteTableOutput
	if err := workflow.ExecuteActivity(opts, acts.CreateRouteTable, activities.CreateRouteTableInput{
		StackName:   input.StackName,
		Environment: input.Environment,
		VpcId:       input.VpcId,
		Name:        input.Name,
		ExtraTags:   input.ExtraTags,
	}).Get(ctx, &rtOut); err != nil {
		return activities.RouteTableOutput{}, err
	}

	routesFuture := workflow.ExecuteActivity(opts, acts.AddRoutes, activities.AddRoutesInput{
		StackName:    input.StackName + "-routes",
		RouteTableId: rtOut.RouteTableId,
		Routes:       input.Routes,
	})
	assocFuture := workflow.ExecuteActivity(opts, acts.AssociateSubnets, activities.AssociateSubnetsInput{
		StackName:    input.StackName + "-assoc",
		RouteTableId: rtOut.RouteTableId,
		SubnetIds:    input.SubnetIds,
	})

	if err := routesFuture.Get(ctx, nil); err != nil {
		return activities.RouteTableOutput{}, err
	}
	if err := assocFuture.Get(ctx, nil); err != nil {
		return activities.RouteTableOutput{}, err
	}

	return activities.RouteTableOutput{RouteTableId: rtOut.RouteTableId}, nil
}
