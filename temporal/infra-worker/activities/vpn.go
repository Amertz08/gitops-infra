package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2clientvpn"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type VpnInput struct {
	ServerCertArn      string   `json:"serverCertArn"`
	ClientCaArn        string   `json:"clientCaArn"`
	OpsVpcId           string   `json:"opsVpcId"`
	OpsPrivateSubnetId string   `json:"opsPrivateSubnetId"` // first private subnet for association
	SpokeVpcCidrs      []string `json:"spokeVpcCidrs"`      // ["10.1.0.0/16", "10.2.0.0/16"]
	ClientCidr         string   `json:"clientCidr"`         // "172.16.0.0/22"
}

type VpnOutputs struct {
	EndpointId string `json:"endpointId"`
}

func (a *InfraActivities) PreviewVpn(ctx context.Context, input VpnInput) error {
	stack, err := a.openVpnStack(ctx, input)
	if err != nil {
		return err
	}
	w := &heartbeatWriter{ctx: ctx}
	_, err = stack.Preview(ctx, optpreview.ProgressStreams(w))
	return err
}

func (a *InfraActivities) UpVpn(ctx context.Context, input VpnInput) (VpnOutputs, error) {
	stack, err := a.openVpnStack(ctx, input)
	if err != nil {
		return VpnOutputs{}, err
	}
	w := &heartbeatWriter{ctx: ctx}
	result, err := stack.Up(ctx, optup.ProgressStreams(w))
	if err != nil {
		return VpnOutputs{}, err
	}
	return VpnOutputs{
		EndpointId: fmt.Sprintf("%v", result.Outputs["endpointId"].Value),
	}, nil
}

func (a *InfraActivities) openVpnStack(ctx context.Context, input VpnInput) (auto.Stack, error) {
	program := vpnProgram(input)
	stack, err := auto.UpsertStackInlineSource(ctx, "main-vpn", a.ProjectName, program)
	if err != nil {
		return auto.Stack{}, err
	}
	if err := a.configureStack(ctx, stack); err != nil {
		return auto.Stack{}, err
	}
	return stack, nil
}

// vpnProgram returns an inline Pulumi program that creates the AWS Client VPN
// endpoint in the ops VPC. Routing to qa/prod is handled by the TGW stack;
// this program only manages the VPN endpoint itself and its associations.
func vpnProgram(input VpnInput) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		tags := pulumi.StringMap{"ManagedBy": pulumi.String("Pulumi")}

		sg, err := ec2.NewSecurityGroup(ctx, "vpn-sg", &ec2.SecurityGroupArgs{
			VpcId:       pulumi.String(input.OpsVpcId),
			Description: pulumi.String("Client VPN endpoint"),
			Egress: ec2.SecurityGroupEgressArray{
				&ec2.SecurityGroupEgressArgs{
					Protocol:   pulumi.String("-1"),
					FromPort:   pulumi.Int(0),
					ToPort:     pulumi.Int(0),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
			},
			Tags: mergeTags(tags, pulumi.StringMap{"Name": pulumi.String("main-vpn-sg")}),
		})
		if err != nil {
			return err
		}

		endpoint, err := ec2clientvpn.NewEndpoint(ctx, "vpn-endpoint", &ec2clientvpn.EndpointArgs{
			ServerCertificateArn: pulumi.String(input.ServerCertArn),
			ClientCidrBlock:      pulumi.String(input.ClientCidr),
			SplitTunnel:          pulumi.Bool(true),
			VpcId:                pulumi.String(input.OpsVpcId),
			SecurityGroupIds:     pulumi.StringArray{sg.ID().ToStringOutput()},
			AuthenticationOptions: ec2clientvpn.EndpointAuthenticationOptionArray{
				&ec2clientvpn.EndpointAuthenticationOptionArgs{
					Type:                    pulumi.String("certificate-authentication"),
					RootCertificateChainArn: pulumi.String(input.ClientCaArn),
				},
			},
			ConnectionLogOptions: &ec2clientvpn.EndpointConnectionLogOptionsArgs{
				Enabled: pulumi.Bool(false),
			},
			Tags: mergeTags(tags, pulumi.StringMap{"Name": pulumi.String("main-vpn")}),
		})
		if err != nil {
			return err
		}

		// Associate with the first ops private subnet. This auto-creates a local
		// Client VPN route for the ops VPC CIDR (10.0.0.0/16).
		assoc, err := ec2clientvpn.NewNetworkAssociation(ctx, "vpn-assoc", &ec2clientvpn.NetworkAssociationArgs{
			ClientVpnEndpointId: endpoint.ID(),
			SubnetId:            pulumi.String(input.OpsPrivateSubnetId),
		})
		if err != nil {
			return err
		}

		// Allow all VPN clients to reach the full 10.0.0.0/8 range (all three VPCs).
		if _, err = ec2clientvpn.NewAuthorizationRule(ctx, "vpn-auth-all", &ec2clientvpn.AuthorizationRuleArgs{
			ClientVpnEndpointId: endpoint.ID(),
			TargetNetworkCidr:   pulumi.String("10.0.0.0/8"),
			AuthorizeAllGroups:  pulumi.Bool(true),
		}, pulumi.DependsOn([]pulumi.Resource{assoc})); err != nil {
			return err
		}

		// Explicit Client VPN routes for spoke VPCs. Traffic exits through the ops
		// subnet and is forwarded to the TGW via the ops private route table.
		for i, cidr := range input.SpokeVpcCidrs {
			if _, err = ec2clientvpn.NewRoute(ctx, fmt.Sprintf("vpn-route-%d", i), &ec2clientvpn.RouteArgs{
				ClientVpnEndpointId:  endpoint.ID(),
				DestinationCidrBlock: pulumi.String(cidr),
				TargetVpcSubnetId:    pulumi.String(input.OpsPrivateSubnetId),
			}, pulumi.DependsOn([]pulumi.Resource{assoc})); err != nil {
				return err
			}
		}

		ctx.Export("endpointId", endpoint.ID())
		return nil
	}
}
