package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2clientvpn"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type ClientVpnArgs struct {
	ServerCertArn string
	// ClientCaArn is the ACM ARN of the CA that signed the client certificates.
	// Import with: aws acm import-certificate --certificate file://pki/ca.crt
	ClientCaArn   string
	OpsVpc        *VpcComponent
	SpokeVpcCidrs []string // CIDRs for qa and prod; routes added via TGW
	ClientCidr    string   // IP pool for VPN clients, e.g. "172.16.0.0/22"
}

type ClientVpnComponent struct {
	pulumi.ResourceState
	EndpointId pulumi.StringOutput
}

func NewClientVpn(ctx *pulumi.Context, name string, args *ClientVpnArgs, opts ...pulumi.ResourceOption) (*ClientVpnComponent, error) {
	component := &ClientVpnComponent{}
	if err := ctx.RegisterComponentResource("gitops:networking:ClientVpn", name, component, opts...); err != nil {
		return nil, err
	}

	tags := pulumi.StringMap{
		"ManagedBy": pulumi.String("Pulumi"),
		"Name":      pulumi.String(name + "-client-vpn"),
	}

	sg, err := ec2.NewSecurityGroup(ctx, name+"-vpn-sg", &ec2.SecurityGroupArgs{
		VpcId:       args.OpsVpc.vpcResource.ID(),
		Description: pulumi.String("Client VPN endpoint"),
		Egress: ec2.SecurityGroupEgressArray{
			&ec2.SecurityGroupEgressArgs{
				Protocol:   pulumi.String("-1"),
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
		Tags: tags,
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}

	endpoint, err := ec2clientvpn.NewEndpoint(ctx, name+"-endpoint", &ec2clientvpn.EndpointArgs{
		ServerCertificateArn: pulumi.String(args.ServerCertArn),
		ClientCidrBlock:      pulumi.String(args.ClientCidr),
		SplitTunnel:          pulumi.Bool(true),
		VpcId:                args.OpsVpc.vpcResource.ID(),
		SecurityGroupIds:     pulumi.StringArray{sg.ID().ToStringOutput()},
		AuthenticationOptions: ec2clientvpn.EndpointAuthenticationOptionArray{
			&ec2clientvpn.EndpointAuthenticationOptionArgs{
				Type:                    pulumi.String("certificate-authentication"),
				RootCertificateChainArn: pulumi.String(args.ClientCaArn),
			},
		},
		ConnectionLogOptions: &ec2clientvpn.EndpointConnectionLogOptionsArgs{
			Enabled: pulumi.Bool(false),
		},
		Tags: tags,
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}

	// Associate with the first private subnet in ops — this auto-creates a local
	// route for the ops VPC CIDR (10.0.0.0/16) in the Client VPN route table.
	assoc, err := ec2clientvpn.NewNetworkAssociation(ctx, name+"-assoc", &ec2clientvpn.NetworkAssociationArgs{
		ClientVpnEndpointId: endpoint.ID(),
		SubnetId:            args.OpsVpc.privateSubnets[0].ID(),
	}, pulumi.Parent(component))
	if err != nil {
		return nil, err
	}

	// Allow VPN clients to reach all VPC CIDRs (10.0.0.0/8 covers ops + qa + prod).
	if _, err = ec2clientvpn.NewAuthorizationRule(ctx, name+"-auth-all", &ec2clientvpn.AuthorizationRuleArgs{
		ClientVpnEndpointId: endpoint.ID(),
		TargetNetworkCidr:   pulumi.String("10.0.0.0/8"),
		AuthorizeAllGroups:  pulumi.Bool(true),
	}, pulumi.Parent(component), pulumi.DependsOn([]pulumi.Resource{assoc})); err != nil {
		return nil, err
	}

	// Add routes for spoke VPC CIDRs. Traffic flows: VPN endpoint → ops VPC private
	// subnet → ops private RT (has TGW route) → TGW → spoke VPC.
	for i, cidr := range args.SpokeVpcCidrs {
		if _, err = ec2clientvpn.NewRoute(ctx, fmt.Sprintf("%s-route-%d", name, i), &ec2clientvpn.RouteArgs{
			ClientVpnEndpointId:  endpoint.ID(),
			DestinationCidrBlock: pulumi.String(cidr),
			TargetVpcSubnetId:    args.OpsVpc.privateSubnets[0].ID(),
		}, pulumi.Parent(component), pulumi.DependsOn([]pulumi.Resource{assoc})); err != nil {
			return nil, err
		}
	}

	component.EndpointId = endpoint.ID().ToStringOutput()
	ctx.RegisterResourceOutputs(component, pulumi.Map{
		"endpointId": component.EndpointId,
	})

	return component, nil
}
