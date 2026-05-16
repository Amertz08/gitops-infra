package workflows

import (
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// VpnWorkflow creates the AWS Client VPN endpoint in the ops VPC:
//
//	Step 1: CreateVpnSecurityGroup
//	Step 2: CreateVpnEndpoint         (needs SG)
//	Step 3: CreateVpnNetworkAssociation (needs endpoint; blocks auth and routes)
//	Step 4: CreateVpnAuthorizationRule | CreateVpnRoutes (parallel)
func VpnWorkflow(ctx workflow.Context, input activities.VpnInput) (activities.VpnOutputs, error) {
	var acts *activities.InfraActivities

	shortCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	// VPN endpoint and network association involve AWS wait periods of 5-10 min.
	longCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	// Step 1: Security Group
	var sgOut activities.CreateVpnSecurityGroupOutput
	if err := workflow.ExecuteActivity(shortCtx, acts.CreateVpnSecurityGroup, activities.CreateVpnSecurityGroupInput{
		StackName: input.StackName + "-sg",
		OpsVpcId:  input.OpsVpcId,
		ExtraTags: input.ExtraTags,
	}).Get(ctx, &sgOut); err != nil {
		return activities.VpnOutputs{}, err
	}

	// Step 2: Endpoint
	var endpointOut activities.CreateVpnEndpointOutput
	if err := workflow.ExecuteActivity(longCtx, acts.CreateVpnEndpoint, activities.CreateVpnEndpointInput{
		StackName:     input.StackName + "-endpoint",
		ServerCertArn: input.ServerCertArn,
		ClientCaArn:   input.ClientCaArn,
		ClientCidr:    input.ClientCidr,
		OpsVpcId:      input.OpsVpcId,
		SgId:          sgOut.SgId,
		ExtraTags:     input.ExtraTags,
	}).Get(ctx, &endpointOut); err != nil {
		return activities.VpnOutputs{}, err
	}

	// Step 3: Network association — must be established before auth rules and routes.
	if err := workflow.ExecuteActivity(longCtx, acts.CreateVpnNetworkAssociation, activities.CreateVpnNetworkAssociationInput{
		StackName:          input.StackName + "-assoc",
		EndpointId:         endpointOut.EndpointId,
		OpsPrivateSubnetId: input.OpsPrivateSubnetId,
		ExtraTags:          input.ExtraTags,
	}).Get(ctx, nil); err != nil {
		return activities.VpnOutputs{}, err
	}

	// Step 4: Authorization rule and spoke routes in parallel.
	authFuture := workflow.ExecuteActivity(shortCtx, acts.CreateVpnAuthorizationRule, activities.CreateVpnAuthorizationRuleInput{
		StackName:  input.StackName + "-auth",
		EndpointId: endpointOut.EndpointId,
		ExtraTags:  input.ExtraTags,
	})
	routesFuture := workflow.ExecuteActivity(shortCtx, acts.CreateVpnRoutes, activities.CreateVpnRoutesInput{
		StackName:          input.StackName + "-routes",
		EndpointId:         endpointOut.EndpointId,
		OpsPrivateSubnetId: input.OpsPrivateSubnetId,
		SpokeVpcCidrs:      input.SpokeVpcCidrs,
		ExtraTags:          input.ExtraTags,
	})

	if err := authFuture.Get(ctx, nil); err != nil {
		return activities.VpnOutputs{}, err
	}
	if err := routesFuture.Get(ctx, nil); err != nil {
		return activities.VpnOutputs{}, err
	}

	return activities.VpnOutputs{EndpointId: endpointOut.EndpointId}, nil
}
