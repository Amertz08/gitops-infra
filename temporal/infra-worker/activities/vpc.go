package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type VpcInput struct {
	StackName          string            `json:"stackName"`          // Pulumi stack name, e.g. "ops-vpc"
	Environment        string            `json:"environment"`        // label for resource names/tags
	CidrBlock          string            `json:"cidrBlock"`          // VPC CIDR
	PublicSubnetCidrs  []string          `json:"publicSubnetCidrs"`  // one CIDR per public subnet
	PrivateSubnetCidrs []string          `json:"privateSubnetCidrs"` // one CIDR per private subnet; len must equal len(Azs)
	Azs                []string          `json:"azs"`                // availability zones; len must equal len(PrivateSubnetCidrs)
	NatPerAz           bool              `json:"natPerAz"`           // true = one NAT GW per public subnet; false = single NAT GW
	ExtraTags          map[string]string `json:"extraTags,omitempty"`
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
	stack, err := auto.UpsertStackInlineSource(ctx, input.StackName, a.ProjectName, program)
	if err != nil {
		return auto.Stack{}, err
	}
	if err := a.configureStack(ctx, stack); err != nil {
		return auto.Stack{}, err
	}
	return stack, nil
}

// vpcProgram builds an inline Pulumi program for a complete VPC:
// public subnets + IGW + NAT Gateway(s) + private subnets + route tables.
// All CIDRs come from the input — no conventions are assumed.
func vpcProgram(input VpcInput) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		baseTags := pulumi.StringMap{
			"Environment": pulumi.String(input.Environment),
			"ManagedBy":   pulumi.String("Pulumi"),
		}
		for k, v := range input.ExtraTags {
			baseTags[k] = pulumi.String(v)
		}
		tag := func(name string) pulumi.StringMap {
			return mergeTags(baseTags, pulumi.StringMap{"Name": pulumi.String(name)})
		}

		vpc, err := ec2.NewVpc(ctx, "vpc", &ec2.VpcArgs{
			CidrBlock:          pulumi.String(input.CidrBlock),
			EnableDnsHostnames: pulumi.Bool(true),
			EnableDnsSupport:   pulumi.Bool(true),
			Tags:               tag(input.Environment + "-vpc"),
		})
		if err != nil {
			return err
		}

		igw, err := ec2.NewInternetGateway(ctx, "igw", &ec2.InternetGatewayArgs{
			VpcId: vpc.ID(),
			Tags:  tag(input.Environment + "-igw"),
		})
		if err != nil {
			return err
		}

		// Public subnets, EIPs, and NAT Gateways.
		publicSubnets := make([]*ec2.Subnet, len(input.PublicSubnetCidrs))
		natGws := make([]*ec2.NatGateway, 0, len(input.PublicSubnetCidrs))

		for i, cidr := range input.PublicSubnetCidrs {
			az := input.Azs[0]
			if i < len(input.Azs) {
				az = input.Azs[i]
			}
			sub, err := ec2.NewSubnet(ctx, fmt.Sprintf("public-%d", i), &ec2.SubnetArgs{
				VpcId:               vpc.ID(),
				CidrBlock:           pulumi.String(cidr),
				AvailabilityZone:    pulumi.String(az),
				MapPublicIpOnLaunch: pulumi.Bool(false),
				Tags:                tag(fmt.Sprintf("%s-public-%d", input.Environment, i)),
			})
			if err != nil {
				return err
			}
			publicSubnets[i] = sub

			// Create a NAT GW for this public subnet when NatPerAz is set,
			// or only for the first subnet when using a single NAT GW.
			if input.NatPerAz || i == 0 {
				eip, err := ec2.NewEip(ctx, fmt.Sprintf("nat-eip-%d", i), &ec2.EipArgs{
					Domain: pulumi.String("vpc"),
					Tags:   tag(fmt.Sprintf("%s-nat-eip-%d", input.Environment, i)),
				})
				if err != nil {
					return err
				}
				nat, err := ec2.NewNatGateway(ctx, fmt.Sprintf("nat-%d", i), &ec2.NatGatewayArgs{
					SubnetId:     sub.ID(),
					AllocationId: eip.ID(),
					Tags:         tag(fmt.Sprintf("%s-nat-%d", input.Environment, i)),
				}, pulumi.DependsOn([]pulumi.Resource{igw}))
				if err != nil {
					return err
				}
				natGws = append(natGws, nat)
			}
		}

		// Public route table — default route to IGW.
		publicRt, err := ec2.NewRouteTable(ctx, "public-rt", &ec2.RouteTableArgs{
			VpcId: vpc.ID(),
			Tags:  tag(input.Environment + "-public-rt"),
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
		for i, sub := range publicSubnets {
			if _, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("public-rta-%d", i), &ec2.RouteTableAssociationArgs{
				SubnetId:     sub.ID(),
				RouteTableId: publicRt.ID(),
			}); err != nil {
				return err
			}
		}

		// Private subnets with route tables pointing to the appropriate NAT GW.
		privateSubnets := make([]*ec2.Subnet, len(input.PrivateSubnetCidrs))
		privateRts := make([]*ec2.RouteTable, len(input.PrivateSubnetCidrs))

		for i, cidr := range input.PrivateSubnetCidrs {
			sub, err := ec2.NewSubnet(ctx, fmt.Sprintf("private-%d", i), &ec2.SubnetArgs{
				VpcId:            vpc.ID(),
				CidrBlock:        pulumi.String(cidr),
				AvailabilityZone: pulumi.String(input.Azs[i]),
				Tags:             tag(fmt.Sprintf("%s-private-%d", input.Environment, i)),
			})
			if err != nil {
				return err
			}
			privateSubnets[i] = sub

			rt, err := ec2.NewRouteTable(ctx, fmt.Sprintf("private-rt-%d", i), &ec2.RouteTableArgs{
				VpcId: vpc.ID(),
				Tags:  tag(fmt.Sprintf("%s-private-rt-%d", input.Environment, i)),
			})
			if err != nil {
				return err
			}
			privateRts[i] = rt

			natGw := natGws[i%len(natGws)]
			if _, err = ec2.NewRoute(ctx, fmt.Sprintf("private-nat-route-%d", i), &ec2.RouteArgs{
				RouteTableId:         rt.ID(),
				DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
				NatGatewayId:         natGw.ID(),
			}); err != nil {
				return err
			}
			if _, err = ec2.NewRouteTableAssociation(ctx, fmt.Sprintf("private-rta-%d", i), &ec2.RouteTableAssociationArgs{
				SubnetId:     sub.ID(),
				RouteTableId: rt.ID(),
			}); err != nil {
				return err
			}
		}

		publicSubnetInputs := make(pulumi.StringArray, len(publicSubnets))
		for i, s := range publicSubnets {
			publicSubnetInputs[i] = s.ID().ToStringOutput()
		}
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
		ctx.Export("publicSubnetIds", publicSubnetInputs)
		ctx.Export("privateRouteTableIds", privateRtInputs)
		return nil
	}
}
