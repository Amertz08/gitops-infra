package tgw

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2transitgateway"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type TransitGatewayOutputs struct {
	TgwId pulumi.IDOutput
}

type VpcAttachmentArgs struct {
	Env string
	// TgwId accepts both pulumi.IDOutput and pulumi.StringOutput.
	TgwId                pulumi.StringInput
	VpcId                pulumi.StringInput
	PrivateSubnetIds     pulumi.StringArrayInput
	PrivateRouteTableIds pulumi.StringArrayOutput
	// DestinationCidrs are added as routes in each private route table pointing to the TGW.
	// Pass the CIDRs of all other VPCs (and the VPN client CIDR for spoke stacks).
	DestinationCidrs []string
}

// NewTransitGateway creates the Transit Gateway. Call this only from the ops stack.
func NewTransitGateway(ctx *pulumi.Context, env string, opts ...pulumi.ResourceOption) (*TransitGatewayOutputs, error) {
	tgw, err := ec2transitgateway.NewTransitGateway(ctx, "main-tgw", &ec2transitgateway.TransitGatewayArgs{
		DefaultRouteTableAssociation: pulumi.String("enable"),
		DefaultRouteTablePropagation: pulumi.String("enable"),
		Tags: pulumi.StringMap{
			"Environment": pulumi.String(env),
			"ManagedBy":   pulumi.String("Pulumi"),
			"Name":        pulumi.String("main-tgw"),
		},
	}, opts...)
	if err != nil {
		return nil, err
	}
	return &TransitGatewayOutputs{TgwId: tgw.ID()}, nil
}

// NewVpcAttachment attaches a VPC to the Transit Gateway and injects TGW routes into the
// VPC's private route tables for each destination CIDR.
func NewVpcAttachment(ctx *pulumi.Context, name string, args VpcAttachmentArgs, opts ...pulumi.ResourceOption) error {
	attachment, err := ec2transitgateway.NewVpcAttachment(ctx, name, &ec2transitgateway.VpcAttachmentArgs{
		TransitGatewayId: args.TgwId,
		VpcId:            args.VpcId,
		SubnetIds:        args.PrivateSubnetIds,
		Tags: pulumi.StringMap{
			"Environment": pulumi.String(args.Env),
			"ManagedBy":   pulumi.String("Pulumi"),
			"Name":        pulumi.String(name),
		},
	}, opts...)
	if err != nil {
		return err
	}

	attachOpts := append(opts, pulumi.DependsOn([]pulumi.Resource{attachment}))

	// Add a route for each destination CIDR in each private route table.
	// Two AZs → two private route tables, indexed 0 and 1.
	rtIds := [2]pulumi.StringOutput{
		args.PrivateRouteTableIds.Index(pulumi.Int(0)),
		args.PrivateRouteTableIds.Index(pulumi.Int(1)),
	}
	for i, cidr := range args.DestinationCidrs {
		for j, rtId := range rtIds {
			_, err = ec2.NewRoute(ctx, fmt.Sprintf("%s-tgw-route-%d-%d", name, i, j), &ec2.RouteArgs{
				RouteTableId:         rtId,
				DestinationCidrBlock: pulumi.String(cidr),
				TransitGatewayId:     attachment.TransitGatewayId.ToStringPtrOutput(),
			}, attachOpts...)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
