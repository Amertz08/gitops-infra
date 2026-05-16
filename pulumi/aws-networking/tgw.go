package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2transitgateway"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type TransitGatewayArgs struct {
	// HubVpc is where the Client VPN endpoint lives (ops). It gets cross-VPC
	// routes to spokes but NOT the VPN client return route.
	HubVpc        *VpcComponent
	SpokeVpcs     []*VpcComponent
	VpnClientCidr string // e.g. "172.16.0.0/22" — added as return route in spoke RTs
}

type TransitGatewayComponent struct {
	pulumi.ResourceState
	TgwId pulumi.StringOutput

	tgw         *ec2transitgateway.TransitGateway
	attachments []*ec2transitgateway.VpcAttachment
}

func NewTransitGateway(ctx *pulumi.Context, name string, args *TransitGatewayArgs, opts ...pulumi.ResourceOption) (*TransitGatewayComponent, error) {
	component := &TransitGatewayComponent{}
	if err := ctx.RegisterComponentResource("gitops:networking:TransitGateway", name, component, opts...); err != nil {
		return nil, err
	}

	tags := pulumi.StringMap{"ManagedBy": pulumi.String("Pulumi")}

	tgw, err := ec2transitgateway.NewTransitGateway(ctx, name+"-tgw", &ec2transitgateway.TransitGatewayArgs{
		DefaultRouteTableAssociation: pulumi.String("enable"),
		DefaultRouteTablePropagation: pulumi.String("enable"),
		Tags:                         mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(name + "-tgw")}),
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}
	component.tgw = tgw

	allVpcs := append([]*VpcComponent{args.HubVpc}, args.SpokeVpcs...)

	// Create VPC attachments — one per VPC, using all private subnets.
	attachments := make([]*ec2transitgateway.VpcAttachment, len(allVpcs))
	for i, vpc := range allVpcs {
		subnetIds := make(pulumi.StringArray, len(vpc.privateSubnets))
		for j, s := range vpc.privateSubnets {
			subnetIds[j] = s.ID().ToStringOutput()
		}
		att, err := ec2transitgateway.NewVpcAttachment(ctx, fmt.Sprintf("%s-attach-%d", name, i), &ec2transitgateway.VpcAttachmentArgs{
			TransitGatewayId: tgw.ID(),
			VpcId:            vpc.vpcResource.ID(),
			SubnetIds:        subnetIds,
			Tags:             mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(fmt.Sprintf("%s-attach-%d", name, i))}),
		}, pulumi.Parent(component))
		if err != nil {
			return nil, err
		}
		attachments[i] = att
	}
	component.attachments = attachments

	// All attachments as a DependsOn slice — routes require the TGW to be fully attached.
	attachDeps := make([]pulumi.Resource, len(attachments))
	for i, a := range attachments {
		attachDeps[i] = a
	}

	// VPC CIDRs indexed to match allVpcs.
	vpcCidrs := []string{"10.0.0.0/16", "10.1.0.0/16", "10.2.0.0/16"}

	for i, vpc := range allVpcs {
		isHub := i == 0
		for rtIdx, rt := range vpc.privateRts {
			// Add a route to every OTHER VPC's CIDR via the TGW.
			for j, cidr := range vpcCidrs {
				if i == j {
					continue
				}
				routeName := fmt.Sprintf("%s-vpc%d-rt%d-to-vpc%d", name, i, rtIdx, j)
				if _, err = ec2.NewRoute(ctx, routeName, &ec2.RouteArgs{
					RouteTableId:         rt.ID(),
					DestinationCidrBlock: pulumi.String(cidr),
					TransitGatewayId:     tgw.ID(),
				}, pulumi.Parent(component), pulumi.DependsOn(attachDeps)); err != nil {
					return nil, err
				}
			}

			// Spoke VPCs need a return route so VPN clients (172.16.0.0/22) can
			// get responses back through the TGW → ops VPC → VPN endpoint.
			if !isHub && args.VpnClientCidr != "" {
				routeName := fmt.Sprintf("%s-vpc%d-rt%d-vpn-return", name, i, rtIdx)
				if _, err = ec2.NewRoute(ctx, routeName, &ec2.RouteArgs{
					RouteTableId:         rt.ID(),
					DestinationCidrBlock: pulumi.String(args.VpnClientCidr),
					TransitGatewayId:     tgw.ID(),
				}, pulumi.Parent(component), pulumi.DependsOn(attachDeps)); err != nil {
					return nil, err
				}
			}
		}
	}

	component.TgwId = tgw.ID().ToStringOutput()
	ctx.RegisterResourceOutputs(component, pulumi.Map{
		"tgwId": component.TgwId,
	})

	return component, nil
}
