package workflows

import (
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
//	Step 3: CreateNatGateways
//	Step 4: CreatePublicRouteTable | CreatePrivateRouteTables (parallel)
func VpcWorkflow(ctx workflow.Context, input activities.VpcInput) (activities.VpcOutputs, error) {
	var acts *activities.InfraActivities

	shortCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	// NAT Gateway provisioning can take ~5 min in AWS; use a longer close timeout.
	longCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
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
	pubFuture := workflow.ExecuteActivity(shortCtx, acts.CreatePublicSubnets, activities.CreatePublicSubnetsInput{
		StackName:   input.StackName + "-public-subnets",
		Environment: input.Environment,
		VpcId:       vpcOut.VpcId,
		SubnetCidrs: input.PublicSubnetCidrs,
		Azs:         input.Azs,
		ExtraTags:   input.ExtraTags,
	})
	privFuture := workflow.ExecuteActivity(shortCtx, acts.CreatePrivateSubnets, activities.CreatePrivateSubnetsInput{
		StackName:   input.StackName + "-private-subnets",
		Environment: input.Environment,
		VpcId:       vpcOut.VpcId,
		SubnetCidrs: input.PrivateSubnetCidrs,
		Azs:         input.Azs,
		ExtraTags:   input.ExtraTags,
	})

	var igwOut activities.CreateIgwOutput
	if err := igwFuture.Get(ctx, &igwOut); err != nil {
		return activities.VpcOutputs{}, err
	}
	var pubOut activities.CreatePublicSubnetsOutput
	if err := pubFuture.Get(ctx, &pubOut); err != nil {
		return activities.VpcOutputs{}, err
	}
	var privOut activities.CreatePrivateSubnetsOutput
	if err := privFuture.Get(ctx, &privOut); err != nil {
		return activities.VpcOutputs{}, err
	}

	// Step 3: NAT Gateways — IGW and public subnets must exist first; sequencing is
	// enforced by the .Get() calls above, not by passing the IGW ID explicitly.
	var natOut activities.CreateNatGatewaysOutput
	if err := workflow.ExecuteActivity(longCtx, acts.CreateNatGateways, activities.CreateNatGatewaysInput{
		StackName:       input.StackName + "-nat",
		Environment:     input.Environment,
		PublicSubnetIds: pubOut.SubnetIds,
		NatPerAz:        input.NatPerAz,
		ExtraTags:       input.ExtraTags,
	}).Get(ctx, &natOut); err != nil {
		return activities.VpcOutputs{}, err
	}

	// Step 4: Public and private route tables in parallel.
	pubRtFuture := workflow.ExecuteActivity(shortCtx, acts.CreatePublicRouteTable, activities.CreatePublicRouteTableInput{
		StackName:       input.StackName + "-public-rt",
		Environment:     input.Environment,
		VpcId:           vpcOut.VpcId,
		IgwId:           igwOut.IgwId,
		PublicSubnetIds: pubOut.SubnetIds,
		ExtraTags:       input.ExtraTags,
	})
	privRtFuture := workflow.ExecuteActivity(shortCtx, acts.CreatePrivateRouteTables, activities.CreatePrivateRouteTablesInput{
		StackName:        input.StackName + "-private-rts",
		Environment:      input.Environment,
		VpcId:            vpcOut.VpcId,
		PrivateSubnetIds: privOut.SubnetIds,
		NatGwIds:         natOut.NatGwIds,
		ExtraTags:        input.ExtraTags,
	})

	if err := pubRtFuture.Get(ctx, nil); err != nil {
		return activities.VpcOutputs{}, err
	}
	var privRtOut activities.CreatePrivateRouteTablesOutput
	if err := privRtFuture.Get(ctx, &privRtOut); err != nil {
		return activities.VpcOutputs{}, err
	}

	return activities.VpcOutputs{
		VpcId:                vpcOut.VpcId,
		CidrBlock:            input.CidrBlock,
		PublicSubnetIds:      pubOut.SubnetIds,
		PrivateSubnetIds:     privOut.SubnetIds,
		PrivateRouteTableIds: privRtOut.RouteTableIds,
	}, nil
}
