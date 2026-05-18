package vpn

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2clientvpn"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type Args struct {
	Env              string
	VpcId            pulumi.StringInput
	PrivateSubnetIds pulumi.StringArrayInput
	ServerCertArn  pulumi.StringInput
	ClientCaArn    pulumi.StringInput
	ClientCidr     string // CIDR assigned to VPN clients, e.g. "172.16.0.0/22"
	AuthorizedCidr string // traffic authorisation CIDR, e.g. "10.0.0.0/8"
	// SpokeVpcCidrs are added as VPN routes so clients can reach spoke VPCs via TGW.
	SpokeVpcCidrs []string
}

type Outputs struct {
	EndpointId pulumi.IDOutput
}

func New(ctx *pulumi.Context, args Args, opts ...pulumi.ResourceOption) (*Outputs, error) {
	env := args.Env

	sg, err := ec2.NewSecurityGroup(ctx, fmt.Sprintf("%s-vpn-sg", env), &ec2.SecurityGroupArgs{
		VpcId:       args.VpcId,
		Description: pulumi.String("Client VPN endpoint"),
		Egress: ec2.SecurityGroupEgressArray{
			&ec2.SecurityGroupEgressArgs{
				Protocol:   pulumi.String("-1"),
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
		Tags: resourceTags(env, fmt.Sprintf("%s-vpn-sg", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	endpoint, err := ec2clientvpn.NewEndpoint(ctx, fmt.Sprintf("%s-vpn-endpoint", env), &ec2clientvpn.EndpointArgs{
		ServerCertificateArn: args.ServerCertArn,
		ClientCidrBlock:      pulumi.String(args.ClientCidr),
		SplitTunnel:          pulumi.Bool(true),
		SecurityGroupIds:     pulumi.StringArray{sg.ID().ToStringOutput()},
		VpcId:                args.VpcId,
		AuthenticationOptions: ec2clientvpn.EndpointAuthenticationOptionArray{
			&ec2clientvpn.EndpointAuthenticationOptionArgs{
				Type:                           pulumi.String("certificate-authentication"),
				RootCertificateChainArn:        args.ClientCaArn,
			},
		},
		ConnectionLogOptions: &ec2clientvpn.EndpointConnectionLogOptionsArgs{
			Enabled: pulumi.Bool(false),
		},
		Tags: resourceTags(env, fmt.Sprintf("%s-vpn-endpoint", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Associate the endpoint with the first private subnet.
	// Additional subnet associations increase availability but are billed per association.
	subnetId := args.PrivateSubnetIds.ToStringArrayOutput().Index(pulumi.Int(0))
	assoc, err := ec2clientvpn.NewNetworkAssociation(ctx, fmt.Sprintf("%s-vpn-assoc", env), &ec2clientvpn.NetworkAssociationArgs{
		ClientVpnEndpointId: endpoint.ID(),
		SubnetId:            subnetId,
	}, append(opts, pulumi.DependsOn([]pulumi.Resource{endpoint}))...)
	if err != nil {
		return nil, err
	}

	assocOpts := append(opts, pulumi.DependsOn([]pulumi.Resource{assoc}))

	_, err = ec2clientvpn.NewAuthorizationRule(ctx, fmt.Sprintf("%s-vpn-auth", env), &ec2clientvpn.AuthorizationRuleArgs{
		ClientVpnEndpointId: endpoint.ID(),
		TargetNetworkCidr:   pulumi.String(args.AuthorizedCidr),
		AuthorizeAllGroups:  pulumi.Bool(true),
	}, assocOpts...)
	if err != nil {
		return nil, err
	}

	for i, cidr := range args.SpokeVpcCidrs {
		_, err = ec2clientvpn.NewRoute(ctx, fmt.Sprintf("%s-vpn-route-%d", env, i), &ec2clientvpn.RouteArgs{
			ClientVpnEndpointId:  endpoint.ID(),
			DestinationCidrBlock: pulumi.String(cidr),
			TargetVpcSubnetId:    subnetId,
		}, assocOpts...)
		if err != nil {
			return nil, err
		}
	}

	return &Outputs{EndpointId: endpoint.ID()}, nil
}

func resourceTags(env, name string) pulumi.StringMap {
	return pulumi.StringMap{
		"Environment": pulumi.String(env),
		"ManagedBy":   pulumi.String("Pulumi"),
		"Name":        pulumi.String(name),
	}
}
