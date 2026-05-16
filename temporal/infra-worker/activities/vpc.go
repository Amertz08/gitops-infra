package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.temporal.io/sdk/activity"
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

type CreatePublicSubnetsInput struct {
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	VpcId       string            `json:"vpcId"`
	SubnetCidrs []string          `json:"subnetCidrs"`
	Azs         []string          `json:"azs"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type CreatePublicSubnetsOutput struct {
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

type CreatePrivateSubnetsInput struct {
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	VpcId       string            `json:"vpcId"`
	SubnetCidrs []string          `json:"subnetCidrs"`
	Azs         []string          `json:"azs"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type CreatePrivateSubnetsOutput struct {
	SubnetIds []string `json:"subnetIds"`
}

type CreatePublicRouteTableInput struct {
	StackName       string            `json:"stackName"`
	Environment     string            `json:"environment"`
	VpcId           string            `json:"vpcId"`
	IgwId           string            `json:"igwId"`
	PublicSubnetIds []string          `json:"publicSubnetIds"`
	ExtraTags       map[string]string `json:"extraTags,omitempty"`
}

type CreatePrivateRouteTablesInput struct {
	StackName        string            `json:"stackName"`
	Environment      string            `json:"environment"`
	VpcId            string            `json:"vpcId"`
	PrivateSubnetIds []string          `json:"privateSubnetIds"`
	NatGwIds         []string          `json:"natGwIds"`
	ExtraTags        map[string]string `json:"extraTags,omitempty"`
}
type CreatePrivateRouteTablesOutput struct {
	RouteTableIds []string `json:"routeTableIds"`
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

func (a *InfraActivities) CreatePublicSubnets(ctx context.Context, input CreatePublicSubnetsInput) (CreatePublicSubnetsOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		ids := make(pulumi.StringArray, len(input.SubnetCidrs))
		for i, cidr := range input.SubnetCidrs {
			az := input.Azs[0]
			if i < len(input.Azs) {
				az = input.Azs[i]
			}
			sub, err := ec2.NewSubnet(pctx, fmt.Sprintf("public-%d", i), &ec2.SubnetArgs{
				VpcId:               pulumi.String(input.VpcId),
				CidrBlock:           pulumi.String(cidr),
				AvailabilityZone:    pulumi.String(az),
				MapPublicIpOnLaunch: pulumi.Bool(false),
				Tags:                mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-public-%d", input.Environment, i))}),
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
		return CreatePublicSubnetsOutput{}, err
	}
	return CreatePublicSubnetsOutput{SubnetIds: extractStringSlice(result.Outputs["subnetIds"])}, nil
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

func (a *InfraActivities) CreatePrivateSubnets(ctx context.Context, input CreatePrivateSubnetsInput) (CreatePrivateSubnetsOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		ids := make(pulumi.StringArray, len(input.SubnetCidrs))
		for i, cidr := range input.SubnetCidrs {
			sub, err := ec2.NewSubnet(pctx, fmt.Sprintf("private-%d", i), &ec2.SubnetArgs{
				VpcId:            pulumi.String(input.VpcId),
				CidrBlock:        pulumi.String(cidr),
				AvailabilityZone: pulumi.String(input.Azs[i]),
				Tags:             mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-private-%d", input.Environment, i))}),
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
		return CreatePrivateSubnetsOutput{}, err
	}
	return CreatePrivateSubnetsOutput{SubnetIds: extractStringSlice(result.Outputs["subnetIds"])}, nil
}

func (a *InfraActivities) CreatePublicRouteTable(ctx context.Context, input CreatePublicRouteTableInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		rt, err := ec2.NewRouteTable(pctx, "public-rt", &ec2.RouteTableArgs{
			VpcId: pulumi.String(input.VpcId),
			Tags:  mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Environment + "-public-rt")}),
		})
		if err != nil {
			return err
		}
		if _, err = ec2.NewRoute(pctx, "public-igw-route", &ec2.RouteArgs{
			RouteTableId:         rt.ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
			GatewayId:            pulumi.String(input.IgwId),
		}); err != nil {
			return err
		}
		for i, subnetId := range input.PublicSubnetIds {
			if _, err = ec2.NewRouteTableAssociation(pctx, fmt.Sprintf("public-rta-%d", i), &ec2.RouteTableAssociationArgs{
				SubnetId:     pulumi.String(subnetId),
				RouteTableId: rt.ID(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (a *InfraActivities) CreatePrivateRouteTables(ctx context.Context, input CreatePrivateRouteTablesInput) (CreatePrivateRouteTablesOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		rtIds := make(pulumi.StringArray, len(input.PrivateSubnetIds))
		for i, subnetId := range input.PrivateSubnetIds {
			rt, err := ec2.NewRouteTable(pctx, fmt.Sprintf("private-rt-%d", i), &ec2.RouteTableArgs{
				VpcId: pulumi.String(input.VpcId),
				Tags:  mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-private-rt-%d", input.Environment, i))}),
			})
			if err != nil {
				return err
			}
			natGwId := input.NatGwIds[i%len(input.NatGwIds)]
			if _, err = ec2.NewRoute(pctx, fmt.Sprintf("private-nat-route-%d", i), &ec2.RouteArgs{
				RouteTableId:         rt.ID(),
				DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
				NatGatewayId:         pulumi.String(natGwId),
			}); err != nil {
				return err
			}
			if _, err = ec2.NewRouteTableAssociation(pctx, fmt.Sprintf("private-rta-%d", i), &ec2.RouteTableAssociationArgs{
				SubnetId:     pulumi.String(subnetId),
				RouteTableId: rt.ID(),
			}); err != nil {
				return err
			}
			rtIds[i] = rt.ID().ToStringOutput()
		}
		pctx.Export("routeTableIds", rtIds)
		return nil
	})
	if err != nil {
		return CreatePrivateRouteTablesOutput{}, err
	}
	return CreatePrivateRouteTablesOutput{RouteTableIds: extractStringSlice(result.Outputs["routeTableIds"])}, nil
}

// upStack opens (or upserts) a Pulumi stack, configures it, and runs Up.
// A background ticker sends a keepalive heartbeat every 30 s so that Temporal
// does not false-timeout the activity during silent AWS provisioning waits.
func (a *InfraActivities) upStack(ctx context.Context, stackName string, program pulumi.RunFunc) (auto.UpResult, error) {
	stack, err := auto.UpsertStackInlineSource(ctx, stackName, a.ProjectName, program)
	if err != nil {
		return auto.UpResult{}, err
	}
	if err := a.configureStack(ctx, stack); err != nil {
		return auto.UpResult{}, err
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				activity.RecordHeartbeat(ctx, stackName)
			case <-stop:
				return
			}
		}
	}()
	result, err := stack.Up(ctx, optup.ProgressStreams(&heartbeatWriter{ctx: ctx}))
	close(stop)
	return result, err
}

// envTags builds the base tag map for resources in a given environment.
func envTags(env string, extra map[string]string) pulumi.StringMap {
	tags := pulumi.StringMap{
		"Environment": pulumi.String(env),
		"ManagedBy":   pulumi.String("Pulumi"),
	}
	for k, v := range extra {
		tags[k] = pulumi.String(v)
	}
	return tags
}
