package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// SecurityGroupRule describes a single ingress or egress rule.
type SecurityGroupRule struct {
	Protocol   string   `json:"protocol"`   // e.g. "-1", "tcp", "udp"
	FromPort   int      `json:"fromPort"`
	ToPort     int      `json:"toPort"`
	CidrBlocks []string `json:"cidrBlocks"`
}

type CreateSecurityGroupInput struct {
	StackName    string              `json:"stackName"`
	Environment  string              `json:"environment"`
	VpcId        string              `json:"vpcId"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	IngressRules []SecurityGroupRule `json:"ingressRules,omitempty"`
	EgressRules  []SecurityGroupRule `json:"egressRules,omitempty"`
	ExtraTags    map[string]string   `json:"extraTags,omitempty"`
}
type CreateSecurityGroupOutput struct {
	SgId string `json:"sgId"`
}

func (a *InfraActivities) CreateSecurityGroup(ctx context.Context, input CreateSecurityGroupInput) (CreateSecurityGroupOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)

		ingress := make(ec2.SecurityGroupIngressArray, len(input.IngressRules))
		for i, r := range input.IngressRules {
			cidrs := make(pulumi.StringArray, len(r.CidrBlocks))
			for j, c := range r.CidrBlocks {
				cidrs[j] = pulumi.String(c)
			}
			ingress[i] = &ec2.SecurityGroupIngressArgs{
				Protocol:   pulumi.String(r.Protocol),
				FromPort:   pulumi.Int(r.FromPort),
				ToPort:     pulumi.Int(r.ToPort),
				CidrBlocks: cidrs,
			}
		}

		egress := make(ec2.SecurityGroupEgressArray, len(input.EgressRules))
		for i, r := range input.EgressRules {
			cidrs := make(pulumi.StringArray, len(r.CidrBlocks))
			for j, c := range r.CidrBlocks {
				cidrs[j] = pulumi.String(c)
			}
			egress[i] = &ec2.SecurityGroupEgressArgs{
				Protocol:   pulumi.String(r.Protocol),
				FromPort:   pulumi.Int(r.FromPort),
				ToPort:     pulumi.Int(r.ToPort),
				CidrBlocks: cidrs,
			}
		}

		sg, err := ec2.NewSecurityGroup(pctx, "sg", &ec2.SecurityGroupArgs{
			VpcId:       pulumi.String(input.VpcId),
			Description: pulumi.String(input.Description),
			Ingress:     ingress,
			Egress:      egress,
			Tags:        mergeTags(tags, pulumi.StringMap{"Name": pulumi.String(input.Name)}),
		})
		if err != nil {
			return err
		}
		pctx.Export("sgId", sg.ID())
		return nil
	})
	if err != nil {
		return CreateSecurityGroupOutput{}, err
	}
	return CreateSecurityGroupOutput{SgId: fmt.Sprintf("%v", result.Outputs["sgId"].Value)}, nil
}
