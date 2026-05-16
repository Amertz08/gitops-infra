package workflows

import (
	"fmt"
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// VpcWorkflow creates a complete VPC by running one activity per resource group.
// Independent resources are provisioned in parallel:
//
//	Step 1: CreateVpc
//	Step 2: CreateIgw | CreatePublicSubnets | CreatePrivateSubnets (parallel)
//	Step 3: NatGatewayWorkflow×N (parallel child workflows; respects NatPerAz)
//	Step 4: RouteTableWorkflow (public) | RouteTableWorkflow×N (private) (parallel child workflows)
func VpcWorkflow(ctx workflow.Context, input activities.VpcInput) (activities.VpcOutputs, error) {
	var acts *activities.InfraActivities

	shortCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	cwo := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	// Step 1: VPC
	var vpcOut activities.CreateVpcOutput
	if err := workflow.ExecuteActivity(shortCtx, acts.CreateVpc, activities.CreateVpcInput{
		StackName:   input.StackName,
		Environment: input.Environment,
		CidrBlock:   input.CidrBlock,
		ExtraTags:   input.ExtraTags,
	}).Get(ctx, &vpcOut); err != nil {
		return activities.VpcOutputs{}, err
	}

	// Step 2: IGW, public subnets, and private subnets in parallel.
	// All three only need VpcId from step 1; none depend on each other.
	igwFuture := workflow.ExecuteActivity(shortCtx, acts.CreateIgw, activities.CreateIgwInput{
		StackName:   input.StackName + "-igw",
		Environment: input.Environment,
		VpcId:       vpcOut.VpcId,
		ExtraTags:   input.ExtraTags,
	})
	pubFuture := workflow.ExecuteActivity(shortCtx, acts.CreateSubnets, activities.CreateSubnetsInput{
		StackName:           input.StackName + "-public-subnets",
		Environment:         input.Environment,
		VpcId:               vpcOut.VpcId,
		SubnetCidrs:         input.PublicSubnetCidrs,
		Azs:                 input.Azs,
		NamePrefix:          input.Environment + "-public",
		MapPublicIpOnLaunch: false,
		ExtraTags:           input.ExtraTags,
	})
	privFuture := workflow.ExecuteActivity(shortCtx, acts.CreateSubnets, activities.CreateSubnetsInput{
		StackName:           input.StackName + "-private-subnets",
		Environment:         input.Environment,
		VpcId:               vpcOut.VpcId,
		SubnetCidrs:         input.PrivateSubnetCidrs,
		Azs:                 input.Azs,
		NamePrefix:          input.Environment + "-private",
		MapPublicIpOnLaunch: false,
		ExtraTags:           input.ExtraTags,
	})

	var igwOut activities.CreateIgwOutput
	if err := igwFuture.Get(ctx, &igwOut); err != nil {
		return activities.VpcOutputs{}, err
	}
	var pubOut activities.CreateSubnetsOutput
	if err := pubFuture.Get(ctx, &pubOut); err != nil {
		return activities.VpcOutputs{}, err
	}
	var privOut activities.CreateSubnetsOutput
	if err := privFuture.Get(ctx, &privOut); err != nil {
		return activities.VpcOutputs{}, err
	}

	// Step 3: NAT Gateways — IGW and public subnets must exist first; sequencing is
	// enforced by the .Get() calls above. NatPerAz controls how many are provisioned.
	subnetsForNat := pubOut.SubnetIds
	if !input.NatPerAz {
		subnetsForNat = pubOut.SubnetIds[:1]
	}
	natFutures := make([]workflow.Future, len(subnetsForNat))
	for i, subnetId := range subnetsForNat {
		natFutures[i] = workflow.ExecuteChildWorkflow(cwo, NatGatewayWorkflow, activities.NatGatewayInput{
			StackName:   fmt.Sprintf("%s-nat-%d", input.StackName, i),
			Environment: input.Environment,
			SubnetId:    subnetId,
			ExtraTags:   input.ExtraTags,
		})
	}
	natGwIds := make([]string, len(natFutures))
	for i, f := range natFutures {
		var out activities.NatGatewayOutput
		if err := f.Get(ctx, &out); err != nil {
			return activities.VpcOutputs{}, err
		}
		natGwIds[i] = out.NatGwId
	}

	// Step 4: Route tables — one per private subnet + one shared public RT, all in parallel
	// as child workflows. RouteTableWorkflow owns the create→route→associate sequence.
	privRtFutures := make([]workflow.Future, len(privOut.SubnetIds))
	for i, subnetId := range privOut.SubnetIds {
		privRtFutures[i] = workflow.ExecuteChildWorkflow(cwo, RouteTableWorkflow, activities.RouteTableInput{
			StackName:   fmt.Sprintf("%s-private-rt-%d", input.StackName, i),
			Environment: input.Environment,
			VpcId:       vpcOut.VpcId,
			Name:        fmt.Sprintf("%s-private-rt-%d", input.Environment, i),
			SubnetIds:   []string{subnetId},
			Routes:      []activities.RouteSpec{{DestCidr: "0.0.0.0/0", NatGatewayId: natGwIds[i%len(natGwIds)]}},
			ExtraTags:   input.ExtraTags,
		})
	}
	pubRtFuture := workflow.ExecuteChildWorkflow(cwo, RouteTableWorkflow, activities.RouteTableInput{
		StackName:   input.StackName + "-public-rt",
		Environment: input.Environment,
		VpcId:       vpcOut.VpcId,
		Name:        input.Environment + "-public-rt",
		SubnetIds:   pubOut.SubnetIds,
		Routes:      []activities.RouteSpec{{DestCidr: "0.0.0.0/0", GatewayId: igwOut.IgwId}},
		ExtraTags:   input.ExtraTags,
	})

	if err := pubRtFuture.Get(ctx, nil); err != nil {
		return activities.VpcOutputs{}, err
	}
	privRtIds := make([]string, len(privRtFutures))
	for i, f := range privRtFutures {
		var out activities.RouteTableOutput
		if err := f.Get(ctx, &out); err != nil {
			return activities.VpcOutputs{}, err
		}
		privRtIds[i] = out.RouteTableId
	}

	return activities.VpcOutputs{
		VpcId:                vpcOut.VpcId,
		CidrBlock:            input.CidrBlock,
		PublicSubnetIds:      pubOut.SubnetIds,
		PrivateSubnetIds:     privOut.SubnetIds,
		PrivateRouteTableIds: privRtIds,
	}, nil
}
