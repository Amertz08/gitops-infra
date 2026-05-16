package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2transitgateway"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type TgwInput struct {
	HubVpc        VpcOutputs   `json:"hubVpc"`        // ops — VPN endpoint lives here
	SpokeVpcs     []VpcOutputs `json:"spokeVpcs"`     // qa, prod
	VpnClientCidr string       `json:"vpnClientCidr"` // e.g. "172.16.0.0/22"
}

type TgwOutputs struct {
	TgwId string `json:"tgwId"`
}

func (a *InfraActivities) PreviewTgw(ctx context.Context, input TgwInput) error {
	stack, err := a.openTgwStack(ctx, input)
	if err != nil {
		return err
	}
	w := &heartbeatWriter{ctx: ctx}
	_, err = stack.Preview(ctx, optpreview.ProgressStreams(w))
	return err
}

func (a *InfraActivities) UpTgw(ctx context.Context, input TgwInput) (TgwOutputs, error) {
	stack, err := a.openTgwStack(ctx, input)
	if err != nil {
		return TgwOutputs{}, err
	}
	w := &heartbeatWriter{ctx: ctx}
	result, err := stack.Up(ctx, optup.ProgressStreams(w))
	if err != nil {
		return TgwOutputs{}, err
	}
	return TgwOutputs{
		TgwId: fmt.Sprintf("%v", result.Outputs["tgwId"].Value),
	}, nil
}

func (a *InfraActivities) openTgwStack(ctx context.Context, input TgwInput) (auto.Stack, error) {
	program := tgwProgram(input)
	stack, err := auto.UpsertStackInlineSource(ctx, "main-tgw", a.ProjectName, program)
	if err != nil {
		return auto.Stack{}, err
	}
	if err := a.configureStack(ctx, stack); err != nil {
		return auto.Stack{}, err
	}
	return stack, nil
}

// tgwProgram returns an inline Pulumi program that:
//   - Creates a Transit Gateway
//   - Attaches all 3 VPCs via private subnets
//   - Adds cross-VPC routes in each VPC's private route tables (using IDs from VpcOutputs)
//   - Adds VPN-client return routes (172.16.0.0/22 → TGW) in spoke VPC route tables
//
// VPC route table resources are referenced by ID — they live in the per-VPC stacks,
// but the Route resources themselves are owned by this stack.
func tgwProgram(input TgwInput) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		tags := pulumi.StringMap{"ManagedBy": pulumi.String("Pulumi")}

		tgw, err := ec2transitgateway.NewTransitGateway(ctx, "tgw", &ec2transitgateway.TransitGatewayArgs{
			DefaultRouteTableAssociation: pulumi.String("enable"),
			DefaultRouteTablePropagation: pulumi.String("enable"),
			Tags:                         mergeTags(tags, pulumi.StringMap{"Name": pulumi.String("main-tgw")}),
		})
		if err != nil {
			return err
		}

		allVpcs := append([]VpcOutputs{input.HubVpc}, input.SpokeVpcs...)
		vpcCidrs := make([]string, len(allVpcs))
		for i, vpc := range allVpcs {
			// Extract CIDR from the VPC's subnet range (10.x.0.0/16).
			if len(vpc.PrivateSubnetIds) > 0 {
				// CIDR is inferred from position: ops=10.0, qa=10.1, prod=10.2
				vpcCidrs[i] = fmt.Sprintf("10.%d.0.0/16", i)
			}
		}

		// Create VPC attachments using private subnets.
		attachments := make([]*ec2transitgateway.VpcAttachment, len(allVpcs))
		for i, vpc := range allVpcs {
			subnetIds := make(pulumi.StringArray, len(vpc.PrivateSubnetIds))
			for j, id := range vpc.PrivateSubnetIds {
				subnetIds[j] = pulumi.String(id)
			}
			att, err := ec2transitgateway.NewVpcAttachment(ctx, fmt.Sprintf("attach-%d", i), &ec2transitgateway.VpcAttachmentArgs{
				TransitGatewayId: tgw.ID(),
				VpcId:            pulumi.String(vpc.VpcId),
				SubnetIds:        subnetIds,
				Tags:             mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("main-tgw-attach-%d", i))}),
			})
			if err != nil {
				return err
			}
			attachments[i] = att
		}

		attachDeps := make([]pulumi.Resource, len(attachments))
		for i, a := range attachments {
			attachDeps[i] = a
		}

		// Add routes in each VPC's private route tables.
		for i, vpc := range allVpcs {
			isHub := i == 0
			for rtIdx, rtId := range vpc.PrivateRouteTableIds {
				// Cross-VPC routes: reach every other VPC via TGW.
				for j, cidr := range vpcCidrs {
					if i == j || cidr == "" {
						continue
					}
					routeName := fmt.Sprintf("vpc%d-rt%d-to-vpc%d", i, rtIdx, j)
					if _, err = ec2.NewRoute(ctx, routeName, &ec2.RouteArgs{
						RouteTableId:         pulumi.String(rtId),
						DestinationCidrBlock: pulumi.String(cidr),
						TransitGatewayId:     tgw.ID(),
					}, pulumi.DependsOn(attachDeps)); err != nil {
						return err
					}
				}

				// Spoke VPCs need a return route so VPN client responses
				// (172.16.x.x) can travel back through TGW → ops VPC → VPN endpoint.
				if !isHub && input.VpnClientCidr != "" {
					routeName := fmt.Sprintf("vpc%d-rt%d-vpn-return", i, rtIdx)
					if _, err = ec2.NewRoute(ctx, routeName, &ec2.RouteArgs{
						RouteTableId:         pulumi.String(rtId),
						DestinationCidrBlock: pulumi.String(input.VpnClientCidr),
						TransitGatewayId:     tgw.ID(),
					}, pulumi.DependsOn(attachDeps)); err != nil {
						return err
					}
				}
			}
		}

		ctx.Export("tgwId", tgw.ID())
		return nil
	}
}
