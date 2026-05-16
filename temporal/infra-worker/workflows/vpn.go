package workflows

import (
	"fmt"
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// VpnWorkflow creates the AWS Client VPN endpoint in the ops VPC:
//
//	Step 1: CreateSecurityGroup
//	Step 2: CreateVpnEndpoint              (needs SG)
//	Step 3: CreateVpnNetworkAssociation    (needs endpoint; blocks auth and routes)
//	Step 4: CreateVpnAuthorizationRule | CreateVpnRoute×N (parallel)
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
	var sgOut activities.CreateSecurityGroupOutput
	if err := workflow.ExecuteActivity(shortCtx, acts.CreateSecurityGroup, activities.CreateSecurityGroupInput{
		StackName:   input.StackName + "-sg",
		Environment: input.Environment,
		VpcId:       input.OpsVpcId,
		Name:        input.StackName + "-sg",
		Description: "Client VPN endpoint",
		EgressRules: []activities.SecurityGroupRule{
			{Protocol: "-1", FromPort: 0, ToPort: 0, CidrBlocks: []string{"0.0.0.0/0"}},
		},
		ExtraTags: input.ExtraTags,
	}).Get(ctx, &sgOut); err != nil {
		return activities.VpnOutputs{}, err
	}

	// Step 2: Endpoint
	var endpointOut activities.CreateVpnEndpointOutput
	if err := workflow.ExecuteActivity(longCtx, acts.CreateVpnEndpoint, activities.CreateVpnEndpointInput{
		StackName:     input.StackName + "-endpoint",
		Environment:   input.Environment,
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
	}).Get(ctx, nil); err != nil {
		return activities.VpnOutputs{}, err
	}

	// Step 4: Authorization rule and one route per spoke VPC CIDR — all in parallel.
	futures := []workflow.Future{
		workflow.ExecuteActivity(shortCtx, acts.CreateVpnAuthorizationRule, activities.CreateVpnAuthorizationRuleInput{
			StackName:      input.StackName + "-auth",
			EndpointId:     endpointOut.EndpointId,
			AuthorizedCidr: input.AuthorizedCidr,
		}),
	}
	for i, cidr := range input.SpokeVpcCidrs {
		futures = append(futures, workflow.ExecuteActivity(shortCtx, acts.CreateVpnRoute, activities.CreateVpnRouteInput{
			StackName:          fmt.Sprintf("%s-route-%d", input.StackName, i),
			EndpointId:         endpointOut.EndpointId,
			OpsPrivateSubnetId: input.OpsPrivateSubnetId,
			DestCidr:           cidr,
		}))
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return activities.VpnOutputs{}, err
		}
	}

	return activities.VpnOutputs{EndpointId: endpointOut.EndpointId}, nil
}
