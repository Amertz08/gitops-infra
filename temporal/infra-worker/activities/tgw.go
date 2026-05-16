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

type CreateVpcAttachmentsInput struct {
	StackName   string            `json:"stackName"` // e.g. "main-tgw-attachments"
	Environment string            `json:"environment"`
	TgwId       string            `json:"tgwId"`
	Vpcs        []VpcOutputs      `json:"vpcs"` // hub at index 0, spokes at 1+
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

// CreateVpcAttachments attaches each VPC to the Transit Gateway via its private subnets.
// Attachments must be established before routes are added; TgwWorkflow sequences these.
func (a *InfraActivities) CreateVpcAttachments(ctx context.Context, input CreateVpcAttachmentsInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
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

