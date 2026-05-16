package activities

import (
	"context"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type VpcInput struct {
	Environment string   `json:"environment"` // "ops" | "qa" | "prod"
	CidrBlock   string   `json:"cidrBlock"`   // "10.0.0.0/16"
	Azs         []string `json:"azs"`         // ["us-east-1a", "us-east-1b", "us-east-1c"]
}

type VpcOutputs struct {
	VpcId                string   `json:"vpcId"`
	PrivateSubnetIds     []string `json:"privateSubnetIds"`
	PublicSubnetIds      []string `json:"publicSubnetIds"`
	PrivateRouteTableIds []string `json:"privateRouteTableIds"`
}

func (a *InfraActivities) PreviewVpc(ctx context.Context, input VpcInput) error {
	stack, err := a.openVpcStack(ctx, input)
	if err != nil {
		return err
	}
	w := &heartbeatWriter{ctx: ctx}
	_, err = stack.Preview(ctx, optpreview.ProgressStreams(w))
	return err
}

func (a *InfraActivities) UpVpc(ctx context.Context, input VpcInput) (VpcOutputs, error) {
	stack, err := a.openVpcStack(ctx, input)
	if err != nil {
		return VpcOutputs{}, err
	}
	w := &heartbeatWriter{ctx: ctx}
	result, err := stack.Up(ctx, optup.ProgressStreams(w))
	if err != nil {
		return VpcOutputs{}, err
	}
	return VpcOutputs{
		VpcId:                fmt.Sprintf("%v", result.Outputs["vpcId"].Value),
		PrivateSubnetIds:     extractStringSlice(result.Outputs["privateSubnetIds"]),
		PublicSubnetIds:      extractStringSlice(result.Outputs["publicSubnetIds"]),
		PrivateRouteTableIds: extractStringSlice(result.Outputs["privateRouteTableIds"]),
	}, nil
}

func (a *InfraActivities) openVpcStack(ctx context.Context, input VpcInput) (auto.Stack, error) {
	program := vpcProgram(input)
	stack, err := auto.UpsertStackInlineSource(ctx, input.Environment+"-vpc", a.ProjectName, program)
	if err != nil {
		return auto.Stack{}, err
	}
	if err := a.configureStack(ctx, stack); err != nil {
		return auto.Stack{}, err
	}
	return stack, nil
}

// vpcProgram returns an inline Pulumi program that creates a complete VPC:
// public subnet + IGW + NAT Gateway + 3 private subnets + route tables.
func vpcProgram(input VpcInput) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		// Extract second octet (10.<x>.0.0/16) to derive subnet CIDRs.
		octets := strings.Split(strings.Split(input.CidrBlock, "/")[0], ".")
		x := octets[1]

		tags := pulumi.StringMap{
			"Environment": pulumi.String(input.Environment),
			"ManagedBy":   pulumi.String("Pulumi"),
		}

		vpc, err := ec2.NewVpc(ctx, "vpc", &ec2.VpcArgs{
			CidrBlock:          pulumi.String(input.CidrBlock),
			EnableDnsHostnames: pulumi.Bool(true),
			EnableDnsSupport:   pulumi.Bool(true),
			Tags:               mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-vpc")}),
		})
		if err != nil {
			return err
		}

		igw, err := ec2.NewInternetGateway(ctx, "igw", &ec2.InternetGatewayArgs{
			VpcId: vpc.ID(),
			Tags:  mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-igw")}),
		})
		if err != nil {
			return err
		}

		// One public subnet in the first AZ — for the NAT Gateway only.
		publicSubnet, err := ec2.NewSubnet(ctx, "public-0", &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String(fmt.Sprintf("10.%s.128.0/24", x)),
			AvailabilityZone:    pulumi.String(input.Azs[0]),
			MapPublicIpOnLaunch: pulumi.Bool(false),
			Tags:                mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-public-0")}),
		})
		if err != nil {
			return err
		}

		eip, err := ec2.NewEip(ctx, "nat-eip", &ec2.EipArgs{
			Domain: pulumi.String("vpc"),
			Tags:   mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-nat-eip")}),
		})
		if err != nil {
			return err
		}

		natGw, err := ec2.NewNatGateway(ctx, "nat", &ec2.NatGatewayArgs{
			SubnetId:     publicSubnet.ID(),
			AllocationId: eip.ID(),
			Tags:         mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-nat")}),
		}, pulumi.DependsOn([]pulumi.Resource{igw}))
		if err != nil {
			return err
		}

		// Public route table — default route to IGW.
		publicRt, err := ec2.NewRouteTable(ctx, "public-rt", &ec2.RouteTableArgs{
			VpcId: vpc.ID(),
			Tags:  mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-public-rt")}),
		})
		if err != nil {
			return err
		}
		if _, err = ec2.NewRoute(ctx, "public-igw-route", &ec2.RouteArgs{
			RouteTableId:         publicRt.ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
			GatewayId:            igw.ID(),
		}); err != nil {
			return err
		}
		if _, err = ec2.NewRouteTableAssociation(ctx, "public-rta-0", &ec2.RouteTableAssociationArgs{
			SubnetId:     publicSubnet.ID(),
			RouteTableId: publicRt.ID(),
		}); err != nil {
			return err
		}

		// Three private subnets across AZs; TGW routes are added by the TGW stack.
		privateCidrs := []string{
			fmt.Sprintf("10.%s.0.0/20", x),
			fmt.Sprintf("10.%s.16.0/20", x),
			fmt.Sprintf("10.%s.32.0/20", x),
		}

		privateSubnets := make([]*ec2.Subnet, len(input.Azs))
		privateRts := make([]*ec2.RouteTable, len(input.Azs))

		for i, az := range input.Azs {
			subnet, err := ec2.NewSubnet(ctx, fmt.Sprintf("private-%d", i), &ec2.SubnetArgs{
				VpcId:            vpc.ID(),
				CidrBlock:        pulumi.String(privateCidrs[i]),
				AvailabilityZone: pulumi.String(az),
				Tags:             mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-private-%d", input.Environment, i))}),
			})
			if err != nil {
				return err
			}
			privateSubnets[i] = subnet

			rt, err := ec2.NewRouteTable(ctx, fmt.Sprintf("private-rt-%d", i), &ec2.RouteTableArgs{
				VpcId: vpc.ID(),
				Tags:  mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-private-rt-%d", input.Environment, i))}),
			})
			if err != nil {
				return err
			}
			privateRts[i] = rt

			if _, err = ec2.NewRoute(ctx, fmt.Sprintf("private-nat-route-%d", i), &ec2.RouteArgs{
				RouteTableId:         rt.ID(),
				DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
				NatGatewayId:         natGw.ID(),
			}); err != nil {
				return err
			}
			if _, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("private-rta-%d", i), &ec2.RouteTableAssociationArgs{
				SubnetId:     subnet.ID(),
				RouteTableId: rt.ID(),
			}); err != nil {
				return err
			}
		}

		// Exports consumed by the TGW and VPN activities.
		privateSubnetInputs := make(pulumi.StringArray, len(privateSubnets))
		for i, s := range privateSubnets {
			privateSubnetInputs[i] = s.ID().ToStringOutput()
		}
		privateRtInputs := make(pulumi.StringArray, len(privateRts))
		for i, rt := range privateRts {
			privateRtInputs[i] = rt.ID().ToStringOutput()
		}

		ctx.Export("vpcId", vpc.ID())
		ctx.Export("privateSubnetIds", privateSubnetInputs)
		ctx.Export("publicSubnetIds", pulumi.StringArray{publicSubnet.ID().ToStringOutput()})
		ctx.Export("privateRouteTableIds", privateRtInputs)
		return nil
	}
}
