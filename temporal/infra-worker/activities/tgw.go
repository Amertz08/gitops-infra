package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2transitgateway"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// TgwInput is the top-level input for TgwWorkflow.
type TgwInput struct {
	StackName     string            `json:"stackName"`     // e.g. "main-tgw"
	HubVpc        VpcOutputs        `json:"hubVpc"`        // ops — VPN endpoint lives here
	SpokeVpcs     []VpcOutputs      `json:"spokeVpcs"`     // qa, prod
	VpnClientCidr string            `json:"vpnClientCidr"` // e.g. "172.16.0.0/22"
	ExtraTags     map[string]string `json:"extraTags,omitempty"`
}

type TgwOutputs struct {
	TgwId string `json:"tgwId"`
}

// --- per-resource activity types ---

type CreateTransitGatewayInput struct {
	StackName string            `json:"stackName"` // e.g. "main-tgw"
	ExtraTags map[string]string `json:"extraTags,omitempty"`
}
type CreateTransitGatewayOutput struct {
	TgwId string `json:"tgwId"`
}

type CreateVpcAttachmentsInput struct {
	StackName string            `json:"stackName"` // e.g. "main-tgw-attachments"
	TgwId     string            `json:"tgwId"`
	Vpcs      []VpcOutputs      `json:"vpcs"` // hub at index 0, spokes at 1+
	ExtraTags map[string]string `json:"extraTags,omitempty"`
}

type AddTgwRoutesInput struct {
	StackName     string            `json:"stackName"` // e.g. "main-tgw-routes"
	TgwId         string            `json:"tgwId"`
	Vpcs          []VpcOutputs      `json:"vpcs"` // hub at index 0, spokes at 1+; each must have CidrBlock set
	VpnClientCidr string            `json:"vpnClientCidr"`
	ExtraTags     map[string]string `json:"extraTags,omitempty"`
}

// --- activity implementations ---

func (a *InfraActivities) CreateTransitGateway(ctx context.Context, input CreateTransitGatewayInput) (CreateTransitGatewayOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := pulumi.StringMap{"ManagedBy": pulumi.String("Pulumi")}
		for k, v := range input.ExtraTags {
			tags[k] = pulumi.String(v)
		}
		tgw, err := ec2transitgateway.NewTransitGateway(pctx, "tgw", &ec2transitgateway.TransitGatewayArgs{
			DefaultRouteTableAssociation: pulumi.String("enable"),
			DefaultRouteTablePropagation: pulumi.String("enable"),
			Tags:                         mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.StackName)}),
		})
		if err != nil {
			return err
		}
		pctx.Export("tgwId", tgw.ID())
		return nil
	})
	if err != nil {
		return CreateTransitGatewayOutput{}, err
	}
	return CreateTransitGatewayOutput{TgwId: fmt.Sprintf("%v", result.Outputs["tgwId"].Value)}, nil
}

// CreateVpcAttachments attaches each VPC to the Transit Gateway via its private subnets.
// Attachments must be established before routes are added; TgwWorkflow sequences these.
func (a *InfraActivities) CreateVpcAttachments(ctx context.Context, input CreateVpcAttachmentsInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := pulumi.StringMap{"ManagedBy": pulumi.String("Pulumi")}
		for k, v := range input.ExtraTags {
			tags[k] = pulumi.String(v)
		}
		for i, vpc := range input.Vpcs {
			subnetIds := make(pulumi.StringArray, len(vpc.PrivateSubnetIds))
			for j, id := range vpc.PrivateSubnetIds {
				subnetIds[j] = pulumi.String(id)
			}
			_, err := ec2transitgateway.NewVpcAttachment(pctx, fmt.Sprintf("attach-%d", i), &ec2transitgateway.VpcAttachmentArgs{
				TransitGatewayId: pulumi.String(input.TgwId),
				VpcId:            pulumi.String(vpc.VpcId),
				SubnetIds:        subnetIds,
				Tags:             mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-attach-%d", input.StackName, i))}),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// AddTgwRoutes programs cross-VPC routes and VPN return routes in each VPC's private
// route tables. Each VpcOutputs in Vpcs must have CidrBlock set. Index 0 is the hub
// (no VPN return route needed); indices 1+ are spokes.
func (a *InfraActivities) AddTgwRoutes(ctx context.Context, input AddTgwRoutesInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		for i, vpc := range input.Vpcs {
			isHub := i == 0
			for rtIdx, rtId := range vpc.PrivateRouteTableIds {
				// Cross-VPC routes: one route per destination VPC.
				for j, dest := range input.Vpcs {
					if i == j || dest.CidrBlock == "" {
						continue
					}
					routeName := fmt.Sprintf("vpc%d-rt%d-to-vpc%d", i, rtIdx, j)
					if _, err := ec2.NewRoute(pctx, routeName, &ec2.RouteArgs{
						RouteTableId:         pulumi.String(rtId),
						DestinationCidrBlock: pulumi.String(dest.CidrBlock),
						TransitGatewayId:     pulumi.String(input.TgwId),
					}); err != nil {
						return err
					}
				}
				// Spoke VPCs need a return route for VPN client traffic.
				if !isHub && input.VpnClientCidr != "" {
					routeName := fmt.Sprintf("vpc%d-rt%d-vpn-return", i, rtIdx)
					if _, err := ec2.NewRoute(pctx, routeName, &ec2.RouteArgs{
						RouteTableId:         pulumi.String(rtId),
						DestinationCidrBlock: pulumi.String(input.VpnClientCidr),
						TransitGatewayId:     pulumi.String(input.TgwId),
					}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	return err
}
