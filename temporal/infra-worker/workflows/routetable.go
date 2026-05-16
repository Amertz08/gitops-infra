package workflows

import (
	"fmt"
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RouteTableWorkflow creates a route table, adds routes, and associates subnets:
//
//	Step 1: CreateRouteTable
//	Step 2: AddRoute×N | AssociateSubnet×N (all in parallel)
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

	var futures []workflow.Future
	for i, route := range input.Routes {
		futures = append(futures, workflow.ExecuteActivity(opts, acts.AddRoute, activities.AddRouteInput{
			StackName:    fmt.Sprintf("%s-route-%d", input.StackName, i),
			RouteTableId: rtOut.RouteTableId,
			Route:        route,
		}))
	}
	for i, subnetId := range input.SubnetIds {
		futures = append(futures, workflow.ExecuteActivity(opts, acts.AssociateSubnet, activities.AssociateSubnetInput{
			StackName:    fmt.Sprintf("%s-assoc-%d", input.StackName, i),
			RouteTableId: rtOut.RouteTableId,
			SubnetId:     subnetId,
		}))
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return activities.RouteTableOutput{}, err
		}
	}

	return activities.RouteTableOutput{RouteTableId: rtOut.RouteTableId}, nil
}
