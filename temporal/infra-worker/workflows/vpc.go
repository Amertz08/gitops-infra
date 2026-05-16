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
	// All only need VpcId from step 1; none depend on each other.
	igwFuture := workflow.ExecuteActivity(shortCtx, acts.CreateIgw, activities.CreateIgwInput{
		StackName:   input.StackName + "-igw",
		Environment: input.Environment,
		VpcId:       vpcOut.VpcId,
		ExtraTags:   input.ExtraTags,
	})
	pubFutures := make([]workflow.Future, len(input.PublicSubnetCidrs))
	for i, cidr := range input.PublicSubnetCidrs {
		pubFutures[i] = workflow.ExecuteActivity(shortCtx, acts.CreateSubnet, activities.CreateSubnetInput{
			StackName:   fmt.Sprintf("%s-public-subnet-%d", input.StackName, i),
			Environment: input.Environment,
			VpcId:       vpcOut.VpcId,
			CidrBlock:   cidr,
			Az:          input.Azs[i],
			Name:        fmt.Sprintf("%s-public-%d", input.Environment, i),
			ExtraTags:   input.ExtraTags,
		})
	}
	privFutures := make([]workflow.Future, len(input.PrivateSubnetCidrs))
	for i, cidr := range input.PrivateSubnetCidrs {
		privFutures[i] = workflow.ExecuteActivity(shortCtx, acts.CreateSubnet, activities.CreateSubnetInput{
			StackName:   fmt.Sprintf("%s-private-subnet-%d", input.StackName, i),
			Environment: input.Environment,
			VpcId:       vpcOut.VpcId,
			CidrBlock:   cidr,
			Az:          input.Azs[i],
			Name:        fmt.Sprintf("%s-private-%d", input.Environment, i),
			ExtraTags:   input.ExtraTags,
		})
	}

	var igwOut activities.CreateIgwOutput
	if err := igwFuture.Get(ctx, &igwOut); err != nil {
		return activities.VpcOutputs{}, err
	}
	pubSubnetIds := make([]string, len(pubFutures))
	for i, f := range pubFutures {
		var out activities.CreateSubnetOutput
		if err := f.Get(ctx, &out); err != nil {
			return activities.VpcOutputs{}, err
		}
		pubSubnetIds[i] = out.SubnetId
	}
	privSubnetIds := make([]string, len(privFutures))
	for i, f := range privFutures {
		var out activities.CreateSubnetOutput
		if err := f.Get(ctx, &out); err != nil {
			return activities.VpcOutputs{}, err
		}
		privSubnetIds[i] = out.SubnetId
	}

	// Step 3: NAT Gateways — IGW and public subnets must exist first; sequencing is
	// enforced by the .Get() calls above. NatPerAz controls how many are provisioned.
	subnetsForNat := pubSubnetIds
	if !input.NatPerAz {
		subnetsForNat = pubSubnetIds[:1]
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
	privRtFutures := make([]workflow.Future, len(privSubnetIds))
	for i, subnetId := range privSubnetIds {
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
		SubnetIds:   pubSubnetIds,
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
		PublicSubnetIds:      pubSubnetIds,
		PrivateSubnetIds:     privSubnetIds,
		PrivateRouteTableIds: privRtIds,
	}, nil
}
