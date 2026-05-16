package workflows

import (
	"fmt"
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TgwWorkflow connects all VPCs via a Transit Gateway:
//
//	Step 1: CreateTransitGateway
//	Step 2: CreateVpcAttachments  (needs TGW)
//	Step 3: AddTgwRoutes          (needs attachments established)
func TgwWorkflow(ctx workflow.Context, input activities.TgwInput) (activities.TgwOutputs, error) {
	var acts *activities.InfraActivities

	shortCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	// TGW creation and VPC attachments each involve AWS wait periods of 1-3 min.
	longCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	// Step 1: Transit Gateway
	var tgwOut activities.CreateTransitGatewayOutput
	if err := workflow.ExecuteActivity(longCtx, acts.CreateTransitGateway, activities.CreateTransitGatewayInput{
		StackName:   input.StackName,
		Environment: input.Environment,
		ExtraTags:   input.ExtraTags,
	}).Get(ctx, &tgwOut); err != nil {
		return activities.TgwOutputs{}, err
	}

	allVpcs := append([]activities.VpcOutputs{input.HubVpc}, input.SpokeVpcs...)

	// Step 2: Attach all VPCs — must complete before routes are programmed.
	if err := workflow.ExecuteActivity(longCtx, acts.CreateVpcAttachments, activities.CreateVpcAttachmentsInput{
		StackName:   input.StackName + "-attachments",
		Environment: input.Environment,
		TgwId:       tgwOut.TgwId,
		Vpcs:        allVpcs,
		ExtraTags:   input.ExtraTags,
	}).Get(ctx, nil); err != nil {
		return activities.TgwOutputs{}, err
	}

	// Step 3: Cross-VPC routes and VPN return routes — one AddRoute activity per route
	// per route table, all in parallel. The workflow owns which routes go in each table.
	var rtFutures []workflow.Future
	for i, vpc := range allVpcs {
		for rtIdx, rtId := range vpc.PrivateRouteTableIds {
			var routes []activities.RouteSpec
			for j, dest := range allVpcs {
				if i == j || dest.CidrBlock == "" {
					continue
				}
				routes = append(routes, activities.RouteSpec{
					DestCidr:         dest.CidrBlock,
					TransitGatewayId: tgwOut.TgwId,
				})
			}
			// Spoke VPCs (index > 0) need a return route for VPN client traffic.
			if i > 0 && input.VpnClientCidr != "" {
				routes = append(routes, activities.RouteSpec{
					DestCidr:         input.VpnClientCidr,
					TransitGatewayId: tgwOut.TgwId,
				})
			}
			for k, route := range routes {
				rtFutures = append(rtFutures, workflow.ExecuteActivity(shortCtx, acts.AddRoute, activities.AddRouteInput{
					StackName:    fmt.Sprintf("%s-route-vpc%d-rt%d-%d", input.StackName, i, rtIdx, k),
					RouteTableId: rtId,
					Route:        route,
				}))
			}
		}
	}
	for _, f := range rtFutures {
		if err := f.Get(ctx, nil); err != nil {
			return activities.TgwOutputs{}, err
		}
	}

	return activities.TgwOutputs{TgwId: tgwOut.TgwId}, nil
}
