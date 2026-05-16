package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// VpcInput is the top-level input for VpcWorkflow. All sub-activity stack names
// are derived from StackName with a suffix (e.g. "ops-vpc-igw").
type VpcInput struct {
	StackName          string            `json:"stackName"`
	Environment        string            `json:"environment"`
	CidrBlock          string            `json:"cidrBlock"`
	PublicSubnetCidrs  []string          `json:"publicSubnetCidrs"`
	PrivateSubnetCidrs []string          `json:"privateSubnetCidrs"` // len must equal len(Azs)
	Azs                []string          `json:"azs"`
	NatPerAz           bool              `json:"natPerAz"` // true = one NAT GW per public subnet
	ExtraTags          map[string]string `json:"extraTags,omitempty"`
}

// VpcOutputs is returned by VpcWorkflow and consumed by TgwWorkflow / VpnWorkflow.
type VpcOutputs struct {
	VpcId                string   `json:"vpcId"`
	CidrBlock            string   `json:"cidrBlock"`
	PrivateSubnetIds     []string `json:"privateSubnetIds"`
	PublicSubnetIds      []string `json:"publicSubnetIds"`
	PrivateRouteTableIds []string `json:"privateRouteTableIds"`
}

// --- per-resource activity types ---

type CreateVpcInput struct {
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	CidrBlock   string            `json:"cidrBlock"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type CreateVpcOutput struct {
	VpcId string `json:"vpcId"`
}

type CreateIgwInput struct {
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	VpcId       string            `json:"vpcId"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type CreateIgwOutput struct {
	IgwId string `json:"igwId"`
}

type CreateSubnetsInput struct {
	StackName           string            `json:"stackName"`
	Environment         string            `json:"environment"`
	VpcId               string            `json:"vpcId"`
	SubnetCidrs         []string          `json:"subnetCidrs"`
	Azs                 []string          `json:"azs"`
	NamePrefix          string            `json:"namePrefix"`
	MapPublicIpOnLaunch bool              `json:"mapPublicIpOnLaunch"`
	ExtraTags           map[string]string `json:"extraTags,omitempty"`
}
type CreateSubnetsOutput struct {
	SubnetIds []string `json:"subnetIds"`
}

type CreateNatGatewaysInput struct {
	StackName       string            `json:"stackName"`
	Environment     string            `json:"environment"`
	PublicSubnetIds []string          `json:"publicSubnetIds"`
	NatPerAz        bool              `json:"natPerAz"`
	ExtraTags       map[string]string `json:"extraTags,omitempty"`
}
type CreateNatGatewaysOutput struct {
	NatGwIds []string `json:"natGwIds"`
}


// RouteSpec describes a single route entry: exactly one of GatewayId,
// NatGatewayId, or TransitGatewayId should be set.
type RouteSpec struct {
	DestCidr         string `json:"destCidr"`
	GatewayId        string `json:"gatewayId,omitempty"`
	NatGatewayId     string `json:"natGatewayId,omitempty"`
	TransitGatewayId string `json:"transitGatewayId,omitempty"`
}

type CreateRouteTableInput struct {
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	VpcId       string            `json:"vpcId"`
	Name        string            `json:"name"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type CreateRouteTableOutput struct {
	RouteTableId string `json:"routeTableId"`
}

type AssociateSubnetsInput struct {
	StackName    string   `json:"stackName"`
	RouteTableId string   `json:"routeTableId"`
	SubnetIds    []string `json:"subnetIds"`
}

// RouteTableInput is the top-level input for RouteTableWorkflow.
type RouteTableInput struct {
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	VpcId       string            `json:"vpcId"`
	Name        string            `json:"name"`
	SubnetIds   []string          `json:"subnetIds"`
	Routes      []RouteSpec       `json:"routes"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type RouteTableOutput struct {
	RouteTableId string `json:"routeTableId"`
}

// --- activity implementations ---

func (a *InfraActivities) CreateVpc(ctx context.Context, input CreateVpcInput) (CreateVpcOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		vpc, err := ec2.NewVpc(pctx, "vpc", &ec2.VpcArgs{
			CidrBlock:          pulumi.String(input.CidrBlock),
			EnableDnsHostnames: pulumi.Bool(true),
			EnableDnsSupport:   pulumi.Bool(true),
			Tags:               mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-vpc")}),
		})
		if err != nil {
			return err
		}
		pctx.Export("vpcId", vpc.ID())
		return nil
	})
	if err != nil {
		return CreateVpcOutput{}, err
	}
	return CreateVpcOutput{VpcId: fmt.Sprintf("%v", result.Outputs["vpcId"].Value)}, nil
}

func (a *InfraActivities) CreateIgw(ctx context.Context, input CreateIgwInput) (CreateIgwOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		igw, err := ec2.NewInternetGateway(pctx, "igw", &ec2.InternetGatewayArgs{
			VpcId: pulumi.String(input.VpcId),
			Tags:  mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-igw")}),
		})
		if err != nil {
			return err
		}
		pctx.Export("igwId", igw.ID())
		return nil
	})
	if err != nil {
		return CreateIgwOutput{}, err
	}
	return CreateIgwOutput{IgwId: fmt.Sprintf("%v", result.Outputs["igwId"].Value)}, nil
}

func (a *InfraActivities) CreateSubnets(ctx context.Context, input CreateSubnetsInput) (CreateSubnetsOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		ids := make(pulumi.StringArray, len(input.SubnetCidrs))
		for i, cidr := range input.SubnetCidrs {
			sub, err := ec2.NewSubnet(pctx, fmt.Sprintf("subnet-%d", i), &ec2.SubnetArgs{
				VpcId:               pulumi.String(input.VpcId),
				CidrBlock:           pulumi.String(cidr),
				AvailabilityZone:    pulumi.String(input.Azs[i]),
				MapPublicIpOnLaunch: pulumi.Bool(input.MapPublicIpOnLaunch),
				Tags:                mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-%d", input.NamePrefix, i))}),
			})
			if err != nil {
				return err
			}
			ids[i] = sub.ID().ToStringOutput()
		}
		pctx.Export("subnetIds", ids)
		return nil
	})
	if err != nil {
		return CreateSubnetsOutput{}, err
	}
	return CreateSubnetsOutput{SubnetIds: extractStringSlice(result.Outputs["subnetIds"])}, nil
}

func (a *InfraActivities) CreateNatGateways(ctx context.Context, input CreateNatGatewaysInput) (CreateNatGatewaysOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		var ids pulumi.StringArray
		for i, subnetId := range input.PublicSubnetIds {
			if !input.NatPerAz && i > 0 {
				break
			}
			eip, err := ec2.NewEip(pctx, fmt.Sprintf("nat-eip-%d", i), &ec2.EipArgs{
				Domain: pulumi.String("vpc"),
				Tags:   mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-nat-eip-%d", input.Environment, i))}),
			})
			if err != nil {
				return err
			}
			nat, err := ec2.NewNatGateway(pctx, fmt.Sprintf("nat-%d", i), &ec2.NatGatewayArgs{
				SubnetId:     pulumi.String(subnetId),
				AllocationId: eip.ID(),
				Tags:         mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-nat-%d", input.Environment, i))}),
			})
			if err != nil {
				return err
			}
			ids = append(ids, nat.ID().ToStringOutput())
		}
		pctx.Export("natGwIds", ids)
		return nil
	})
	if err != nil {
		return CreateNatGatewaysOutput{}, err
	}
	return CreateNatGatewaysOutput{NatGwIds: extractStringSlice(result.Outputs["natGwIds"])}, nil
}


func (a *InfraActivities) CreateRouteTable(ctx context.Context, input CreateRouteTableInput) (CreateRouteTableOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		rt, err := ec2.NewRouteTable(pctx, "rt", &ec2.RouteTableArgs{
			VpcId: pulumi.String(input.VpcId),
			Tags:  mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Name)}),
		})
		if err != nil {
			return err
		}
		pctx.Export("routeTableId", rt.ID())
		return nil
	})
	if err != nil {
		return CreateRouteTableOutput{}, err
	}
	return CreateRouteTableOutput{RouteTableId: fmt.Sprintf("%v", result.Outputs["routeTableId"].Value)}, nil
}

func (a *InfraActivities) AssociateSubnets(ctx context.Context, input AssociateSubnetsInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		for i, subnetId := range input.SubnetIds {
			if _, err := ec2.NewRouteTableAssociation(pctx, fmt.Sprintf("rta-%d", i), &ec2.RouteTableAssociationArgs{
				SubnetId:     pulumi.String(subnetId),
				RouteTableId: pulumi.String(input.RouteTableId),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

type AddRoutesInput struct {
	StackName    string      `json:"stackName"`
	RouteTableId string      `json:"routeTableId"`
	Routes       []RouteSpec `json:"routes"`
}

func (a *InfraActivities) AddRoutes(ctx context.Context, input AddRoutesInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		for i, spec := range input.Routes {
			args := &ec2.RouteArgs{
				RouteTableId:         pulumi.String(input.RouteTableId),
				DestinationCidrBlock: pulumi.String(spec.DestCidr),
			}
			if spec.GatewayId != "" {
				args.GatewayId = pulumi.String(spec.GatewayId)
			}
			if spec.NatGatewayId != "" {
				args.NatGatewayId = pulumi.String(spec.NatGatewayId)
			}
			if spec.TransitGatewayId != "" {
				args.TransitGatewayId = pulumi.String(spec.TransitGatewayId)
			}
			if _, err := ec2.NewRoute(pctx, fmt.Sprintf("route-%d", i), args); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

