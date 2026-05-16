package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2clientvpn"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// VpnInput is the top-level input for VpnWorkflow.
type VpnInput struct {
	StackName          string            `json:"stackName"`          // e.g. "main-vpn"
	ServerCertArn      string            `json:"serverCertArn"`
	ClientCaArn        string            `json:"clientCaArn"`
	OpsVpcId           string            `json:"opsVpcId"`
	OpsPrivateSubnetId string            `json:"opsPrivateSubnetId"` // subnet for endpoint association
	SpokeVpcCidrs      []string          `json:"spokeVpcCidrs"`
	ClientCidr         string            `json:"clientCidr"` // e.g. "172.16.0.0/22"
	ExtraTags          map[string]string `json:"extraTags,omitempty"`
}

type VpnOutputs struct {
	EndpointId string `json:"endpointId"`
}

// --- per-resource activity types ---

type CreateVpnSecurityGroupInput struct {
	StackName string            `json:"stackName"` // e.g. "main-vpn-sg"
	OpsVpcId  string            `json:"opsVpcId"`
	ExtraTags map[string]string `json:"extraTags,omitempty"`
}
type CreateVpnSecurityGroupOutput struct {
	SgId string `json:"sgId"`
}

type CreateVpnEndpointInput struct {
	StackName     string            `json:"stackName"` // e.g. "main-vpn-endpoint"
	ServerCertArn string            `json:"serverCertArn"`
	ClientCaArn   string            `json:"clientCaArn"`
	ClientCidr    string            `json:"clientCidr"`
	OpsVpcId      string            `json:"opsVpcId"`
	SgId          string            `json:"sgId"`
	ExtraTags     map[string]string `json:"extraTags,omitempty"`
}
type CreateVpnEndpointOutput struct {
	EndpointId string `json:"endpointId"`
}

type CreateVpnNetworkAssociationInput struct {
	StackName          string            `json:"stackName"` // e.g. "main-vpn-assoc"
	EndpointId         string            `json:"endpointId"`
	OpsPrivateSubnetId string            `json:"opsPrivateSubnetId"`
	ExtraTags          map[string]string `json:"extraTags,omitempty"`
}

type CreateVpnAuthorizationRuleInput struct {
	StackName  string            `json:"stackName"` // e.g. "main-vpn-auth"
	EndpointId string            `json:"endpointId"`
	ExtraTags  map[string]string `json:"extraTags,omitempty"`
}

type CreateVpnRoutesInput struct {
	StackName          string            `json:"stackName"` // e.g. "main-vpn-routes"
	EndpointId         string            `json:"endpointId"`
	OpsPrivateSubnetId string            `json:"opsPrivateSubnetId"`
	SpokeVpcCidrs      []string          `json:"spokeVpcCidrs"`
	ExtraTags          map[string]string `json:"extraTags,omitempty"`
}

// --- activity implementations ---

func (a *InfraActivities) CreateVpnSecurityGroup(ctx context.Context, input CreateVpnSecurityGroupInput) (CreateVpnSecurityGroupOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := pulumi.StringMap{"ManagedBy": pulumi.String("Pulumi")}
		for k, v := range input.ExtraTags {
			tags[k] = pulumi.String(v)
		}
		sg, err := ec2.NewSecurityGroup(pctx, "vpn-sg", &ec2.SecurityGroupArgs{
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
			Tags: mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.StackName)}),
		})
		if err != nil {
			return err
		}
		pctx.Export("sgId", sg.ID())
		return nil
	})
	if err != nil {
		return CreateVpnSecurityGroupOutput{}, err
	}
	return CreateVpnSecurityGroupOutput{SgId: fmt.Sprintf("%v", result.Outputs["sgId"].Value)}, nil
}

func (a *InfraActivities) CreateVpnEndpoint(ctx context.Context, input CreateVpnEndpointInput) (CreateVpnEndpointOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := pulumi.StringMap{"ManagedBy": pulumi.String("Pulumi")}
		for k, v := range input.ExtraTags {
			tags[k] = pulumi.String(v)
		}
		endpoint, err := ec2clientvpn.NewEndpoint(pctx, "vpn-endpoint", &ec2clientvpn.EndpointArgs{
			ServerCertificateArn: pulumi.String(input.ServerCertArn),
			ClientCidrBlock:      pulumi.String(input.ClientCidr),
			SplitTunnel:          pulumi.Bool(true),
			VpcId:                pulumi.String(input.OpsVpcId),
			SecurityGroupIds:     pulumi.StringArray{pulumi.String(input.SgId)},
			AuthenticationOptions: ec2clientvpn.EndpointAuthenticationOptionArray{
				&ec2clientvpn.EndpointAuthenticationOptionArgs{
					Type:                    pulumi.String("certificate-authentication"),
					RootCertificateChainArn: pulumi.String(input.ClientCaArn),
				},
			},
			ConnectionLogOptions: &ec2clientvpn.EndpointConnectionLogOptionsArgs{
				Enabled: pulumi.Bool(false),
			},
			Tags: mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.StackName)}),
		})
		if err != nil {
			return err
		}
		pctx.Export("endpointId", endpoint.ID())
		return nil
	})
	if err != nil {
		return CreateVpnEndpointOutput{}, err
	}
	return CreateVpnEndpointOutput{EndpointId: fmt.Sprintf("%v", result.Outputs["endpointId"].Value)}, nil
}

// CreateVpnNetworkAssociation associates the endpoint with the ops private subnet.
// This must complete before authorization rules and routes are created.
func (a *InfraActivities) CreateVpnNetworkAssociation(ctx context.Context, input CreateVpnNetworkAssociationInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		_, err := ec2clientvpn.NewNetworkAssociation(pctx, "vpn-assoc", &ec2clientvpn.NetworkAssociationArgs{
			ClientVpnEndpointId: pulumi.String(input.EndpointId),
			SubnetId:            pulumi.String(input.OpsPrivateSubnetId),
		})
		return err
	})
	return err
}

// CreateVpnAuthorizationRule allows all VPN clients to reach the full 10.0.0.0/8 range.
func (a *InfraActivities) CreateVpnAuthorizationRule(ctx context.Context, input CreateVpnAuthorizationRuleInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		_, err := ec2clientvpn.NewAuthorizationRule(pctx, "vpn-auth-all", &ec2clientvpn.AuthorizationRuleArgs{
			ClientVpnEndpointId: pulumi.String(input.EndpointId),
			TargetNetworkCidr:   pulumi.String("10.0.0.0/8"),
			AuthorizeAllGroups:  pulumi.Bool(true),
		})
		return err
	})
	return err
}

// CreateVpnRoutes adds explicit Client VPN routes for each spoke VPC CIDR.
// Traffic exits through the ops subnet and is forwarded to the TGW.
func (a *InfraActivities) CreateVpnRoutes(ctx context.Context, input CreateVpnRoutesInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		for i, cidr := range input.SpokeVpcCidrs {
			_, err := ec2clientvpn.NewRoute(pctx, fmt.Sprintf("vpn-route-%d", i), &ec2clientvpn.RouteArgs{
				ClientVpnEndpointId:  pulumi.String(input.EndpointId),
				DestinationCidrBlock: pulumi.String(cidr),
				TargetVpcSubnetId:    pulumi.String(input.OpsPrivateSubnetId),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	return err
}
