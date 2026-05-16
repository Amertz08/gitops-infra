package main

import (
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type VpcArgs struct {
	CidrBlock   string
	Azs         []string
	Environment string
}

type VpcComponent struct {
	pulumi.ResourceState

	VpcId                pulumi.StringOutput
	PrivateSubnetIds     pulumi.StringArrayOutput
	PublicSubnetIds      pulumi.StringArrayOutput
	PrivateRouteTableIds pulumi.StringArrayOutput

	// Concrete resources used by tgw.go and vpn.go within this program.
	vpcResource    *ec2.Vpc
	privateSubnets []*ec2.Subnet
	privateRts     []*ec2.RouteTable
}

func NewVpc(ctx *pulumi.Context, name string, args *VpcArgs, opts ...pulumi.ResourceOption) (*VpcComponent, error) {
	component := &VpcComponent{}
	if err := ctx.RegisterComponentResource("gitops:networking:Vpc", name, component, opts...); err != nil {
		return nil, err
	}

	baseTags := pulumi.StringMap{
		"Environment": pulumi.String(args.Environment),
		"ManagedBy":   pulumi.String("Pulumi"),
	}

	vpc, err := ec2.NewVpc(ctx, name+"-vpc", &ec2.VpcArgs{
		CidrBlock:          pulumi.String(args.CidrBlock),
		EnableDnsHostnames: pulumi.Bool(true),
		EnableDnsSupport:   pulumi.Bool(true),
		Tags:               mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(name + "-vpc")}),
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}
	component.vpcResource = vpc

	igw, err := ec2.NewInternetGateway(ctx, name+"-igw", &ec2.InternetGatewayArgs{
		VpcId: vpc.ID(),
		Tags:  mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(name + "-igw")}),
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}

	// Derive subnet second octet from VPC CIDR (10.<x>.0.0/16 → <x>).
	octets := strings.Split(strings.Split(args.CidrBlock, "/")[0], ".")
	x := octets[1]

	// One public subnet in the first AZ — only for the NAT Gateway.
	publicSubnet, err := ec2.NewSubnet(ctx, name+"-public-0", &ec2.SubnetArgs{
		VpcId:               vpc.ID(),
		CidrBlock:           pulumi.String(fmt.Sprintf("10.%s.128.0/24", x)),
		AvailabilityZone:    pulumi.String(args.Azs[0]),
		MapPublicIpOnLaunch: pulumi.Bool(false),
		Tags:                mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(name + "-public-0")}),
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}

	eip, err := ec2.NewEip(ctx, name+"-nat-eip", &ec2.EipArgs{
		Domain: pulumi.String("vpc"),
		Tags:   mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(name + "-nat-eip")}),
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}

	natGw, err := ec2.NewNatGateway(ctx, name+"-nat", &ec2.NatGatewayArgs{
		SubnetId:     publicSubnet.ID(),
		AllocationId: eip.ID(),
		Tags:         mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(name + "-nat")}),
	}, pulumi.Parent(component), pulumi.DependsOn([]pulumi.Resource{igw}))
	if err != nil {
		return nil, err
	}

	// Public route table: default route → IGW.
	publicRt, err := ec2.NewRouteTable(ctx, name+"-public-rt", &ec2.RouteTableArgs{
		VpcId: vpc.ID(),
		Tags:  mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(name + "-public-rt")}),
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}
	if _, err = ec2.NewRoute(ctx, name+"-public-igw-route", &ec2.RouteArgs{
		RouteTableId:         publicRt.ID(),
		DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
		GatewayId:            igw.ID(),
	}, pulumi.Parent(component)); err != nil {
		return nil, err
	}
	if _, err = ec2.NewRouteTableAssociation(ctx, name+"-public-rta-0", &ec2.RouteTableAssociationArgs{
		SubnetId:     publicSubnet.ID(),
		RouteTableId: publicRt.ID(),
	}, pulumi.Parent(component)); err != nil {
		return nil, err
	}

	// Private subnets: /20 per AZ, leaving room for TGW routes added later.
	privateCidrs := []string{
		fmt.Sprintf("10.%s.0.0/20", x),
		fmt.Sprintf("10.%s.16.0/20", x),
		fmt.Sprintf("10.%s.32.0/20", x),
	}

	privateSubnets := make([]*ec2.Subnet, len(args.Azs))
	privateRts := make([]*ec2.RouteTable, len(args.Azs))

	for i, az := range args.Azs {
		subnet, err := ec2.NewSubnet(ctx, fmt.Sprintf("%s-private-%d", name, i), &ec2.SubnetArgs{
			VpcId:            vpc.ID(),
			CidrBlock:        pulumi.String(privateCidrs[i]),
			AvailabilityZone: pulumi.String(az),
			Tags:             mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-private-%d", name, i))}),
		}, pulumi.Parent(component))
		if err != nil {
			return nil, err
		}
		privateSubnets[i] = subnet

		rt, err := ec2.NewRouteTable(ctx, fmt.Sprintf("%s-private-rt-%d", name, i), &ec2.RouteTableArgs{
			VpcId: vpc.ID(),
			Tags:  mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-private-rt-%d", name, i))}),
		}, pulumi.Parent(component))
		if err != nil {
			return nil, err
		}
		privateRts[i] = rt

		// Default route to NAT Gateway for outbound internet.
		if _, err = ec2.NewRoute(ctx, fmt.Sprintf("%s-private-nat-route-%d", name, i), &ec2.RouteArgs{
			RouteTableId:         rt.ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
			NatGatewayId:         natGw.ID(),
		}, pulumi.Parent(component)); err != nil {
			return nil, err
		}

		if _, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("%s-private-rta-%d", name, i), &ec2.RouteTableAssociationArgs{
			SubnetId:     subnet.ID(),
			RouteTableId: rt.ID(),
		}, pulumi.Parent(component)); err != nil {
			return nil, err
		}
	}

	component.privateSubnets = privateSubnets
	component.privateRts = privateRts

	privateSubnetInputs := make(pulumi.StringArray, len(privateSubnets))
	for i, s := range privateSubnets {
		privateSubnetInputs[i] = s.ID().ToStringOutput()
	}
	privateRtInputs := make(pulumi.StringArray, len(privateRts))
	for i, rt := range privateRts {
		privateRtInputs[i] = rt.ID().ToStringOutput()
	}

	component.VpcId = vpc.ID().ToStringOutput()
	component.PrivateSubnetIds = privateSubnetInputs.ToStringArrayOutput()
	component.PublicSubnetIds = pulumi.StringArray{publicSubnet.ID().ToStringOutput()}.ToStringArrayOutput()
	component.PrivateRouteTableIds = privateRtInputs.ToStringArrayOutput()

	ctx.RegisterResourceOutputs(component, pulumi.Map{
		"vpcId":                component.VpcId,
		"privateSubnetIds":     component.PrivateSubnetIds,
		"publicSubnetIds":      component.PublicSubnetIds,
		"privateRouteTableIds": component.PrivateRouteTableIds,
	})

	return component, nil
}

func mergeTags(base pulumi.StringMap, extra pulumi.StringMap) pulumi.StringMap {
	result := pulumi.StringMap{}
	for k, v := range base {
		result[k] = v
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}
