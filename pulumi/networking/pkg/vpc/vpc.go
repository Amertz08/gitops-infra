package vpc

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type Args struct {
	Env                string
	CidrBlock          string
	AvailabilityZones  []string
	PublicSubnetCidrs  []string
	PrivateSubnetCidrs []string
}

type Outputs struct {
	VpcId                pulumi.IDOutput
	PublicSubnetIds      pulumi.StringArrayOutput
	PrivateSubnetIds     pulumi.StringArrayOutput
	PrivateRouteTableIds pulumi.StringArrayOutput
}

func New(ctx *pulumi.Context, args Args, opts ...pulumi.ResourceOption) (*Outputs, error) {
	env := args.Env

	v, err := ec2.NewVpc(ctx, fmt.Sprintf("%s-vpc", env), &ec2.VpcArgs{
		CidrBlock:          pulumi.String(args.CidrBlock),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags:               resourceTags(env, fmt.Sprintf("%s-vpc", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	igw, err := ec2.NewInternetGateway(ctx, fmt.Sprintf("%s-igw", env), &ec2.InternetGatewayArgs{
		VpcId: v.ID(),
		Tags:  resourceTags(env, fmt.Sprintf("%s-igw", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Public subnets
	publicSubnets := make([]*ec2.Subnet, len(args.PublicSubnetCidrs))
	for i, cidr := range args.PublicSubnetCidrs {
		s, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-public-subnet-%d", env, i), &ec2.SubnetArgs{
			VpcId:               v.ID(),
			CidrBlock:           pulumi.String(cidr),
			AvailabilityZone:    pulumi.String(args.AvailabilityZones[i]),
			MapPublicIpOnLaunch: pulumi.Bool(true),
			Tags:                resourceTags(env, fmt.Sprintf("%s-public-%s", env, args.AvailabilityZones[i])),
		}, opts...)
		if err != nil {
			return nil, err
		}
		publicSubnets[i] = s
	}

	// Private subnets with per-AZ NAT gateways
	privateSubnets := make([]*ec2.Subnet, len(args.PrivateSubnetCidrs))
	natGateways := make([]*ec2.NatGateway, len(args.PrivateSubnetCidrs))
	natOpts := append(opts, pulumi.DependsOn([]pulumi.Resource{igw}))

	for i, cidr := range args.PrivateSubnetCidrs {
		s, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-private-subnet-%d", env, i), &ec2.SubnetArgs{
			VpcId:            v.ID(),
			CidrBlock:        pulumi.String(cidr),
			AvailabilityZone: pulumi.String(args.AvailabilityZones[i]),
			Tags:             resourceTags(env, fmt.Sprintf("%s-private-%s", env, args.AvailabilityZones[i])),
		}, opts...)
		if err != nil {
			return nil, err
		}
		privateSubnets[i] = s

		eip, err := ec2.NewEip(ctx, fmt.Sprintf("%s-eip-%d", env, i), &ec2.EipArgs{
			Domain: pulumi.String("vpc"),
			Tags:   resourceTags(env, fmt.Sprintf("%s-nat-eip-%d", env, i)),
		}, opts...)
		if err != nil {
			return nil, err
		}

		nat, err := ec2.NewNatGateway(ctx, fmt.Sprintf("%s-nat-%d", env, i), &ec2.NatGatewayArgs{
			SubnetId:     publicSubnets[i].ID(),
			AllocationId: eip.ID(),
			Tags:         resourceTags(env, fmt.Sprintf("%s-nat-%d", env, i)),
		}, natOpts...)
		if err != nil {
			return nil, err
		}
		natGateways[i] = nat
	}

	// Public route table — single, shared across all public subnets
	publicRt, err := ec2.NewRouteTable(ctx, fmt.Sprintf("%s-public-rt", env), &ec2.RouteTableArgs{
		VpcId: v.ID(),
		Tags:  resourceTags(env, fmt.Sprintf("%s-public-rt", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	_, err = ec2.NewRoute(ctx, fmt.Sprintf("%s-public-igw-route", env), &ec2.RouteArgs{
		RouteTableId:         publicRt.ID(),
		DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
		GatewayId:            igw.ID(),
	}, opts...)
	if err != nil {
		return nil, err
	}

	for i, s := range publicSubnets {
		_, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("%s-public-rta-%d", env, i), &ec2.RouteTableAssociationArgs{
			SubnetId:     s.ID(),
			RouteTableId: publicRt.ID(),
		}, opts...)
		if err != nil {
			return nil, err
		}
	}

	// Private route tables — one per AZ, each with its own NAT gateway route
	privateRts := make([]*ec2.RouteTable, len(privateSubnets))
	for i, s := range privateSubnets {
		rt, err := ec2.NewRouteTable(ctx, fmt.Sprintf("%s-private-rt-%d", env, i), &ec2.RouteTableArgs{
			VpcId: v.ID(),
			Tags:  resourceTags(env, fmt.Sprintf("%s-private-rt-%d", env, i)),
		}, opts...)
		if err != nil {
			return nil, err
		}

		_, err = ec2.NewRoute(ctx, fmt.Sprintf("%s-private-nat-route-%d", env, i), &ec2.RouteArgs{
			RouteTableId:         rt.ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
			NatGatewayId:         natGateways[i].ID(),
		}, opts...)
		if err != nil {
			return nil, err
		}

		_, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("%s-private-rta-%d", env, i), &ec2.RouteTableAssociationArgs{
			SubnetId:     s.ID(),
			RouteTableId: rt.ID(),
		}, opts...)
		if err != nil {
			return nil, err
		}

		privateRts[i] = rt
	}

	// Collect IDs into StringArrayOutputs
	var publicIds pulumi.StringArray
	for _, s := range publicSubnets {
		publicIds = append(publicIds, s.ID().ToStringOutput())
	}

	var privateIds pulumi.StringArray
	for _, s := range privateSubnets {
		privateIds = append(privateIds, s.ID().ToStringOutput())
	}

	var privateRtIds pulumi.StringArray
	for _, rt := range privateRts {
		privateRtIds = append(privateRtIds, rt.ID().ToStringOutput())
	}

	return &Outputs{
		VpcId:                v.ID(),
		PublicSubnetIds:      publicIds.ToStringArrayOutput(),
		PrivateSubnetIds:     privateIds.ToStringArrayOutput(),
		PrivateRouteTableIds: privateRtIds.ToStringArrayOutput(),
	}, nil
}

func resourceTags(env, name string) pulumi.StringMap {
	return pulumi.StringMap{
		"Environment": pulumi.String(env),
		"ManagedBy":   pulumi.String("Pulumi"),
		"Name":        pulumi.String(name),
	}
}
