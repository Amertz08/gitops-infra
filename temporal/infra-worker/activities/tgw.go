package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2transitgateway"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// TgwInput is the top-level input for TgwWorkflow.
type TgwInput struct {
	StackName     string            `json:"stackName"`     // e.g. "main-tgw"
	Environment   string            `json:"environment"`   // e.g. "shared"
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
	StackName   string            `json:"stackName"` // e.g. "main-tgw"
	Environment string            `json:"environment"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type CreateTransitGatewayOutput struct {
	TgwId string `json:"tgwId"`
}

type CreateVpcAttachmentInput struct {
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	TgwId       string            `json:"tgwId"`
	Vpc         VpcOutputs        `json:"vpc"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}


// --- activity implementations ---

func (a *InfraActivities) CreateTransitGateway(ctx context.Context, input CreateTransitGatewayInput) (CreateTransitGatewayOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
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

// CreateVpcAttachment attaches a single VPC to the Transit Gateway via its private subnets.
// All attachments must be established before routes are added; TgwWorkflow sequences these.
func (a *InfraActivities) CreateVpcAttachment(ctx context.Context, input CreateVpcAttachmentInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		subnetIds := make(pulumi.StringArray, len(input.Vpc.PrivateSubnetIds))
		for i, id := range input.Vpc.PrivateSubnetIds {
			subnetIds[i] = pulumi.String(id)
		}
		_, err := ec2transitgateway.NewVpcAttachment(pctx, "attach", &ec2transitgateway.VpcAttachmentArgs{
			TransitGatewayId: pulumi.String(input.TgwId),
			VpcId:            pulumi.String(input.Vpc.VpcId),
			SubnetIds:        subnetIds,
			Tags:             mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.StackName)}),
		})
		return err
	})
	return err
}

