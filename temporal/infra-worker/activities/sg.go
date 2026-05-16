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
	StackName   string            `json:"stackName"`
	Environment string            `json:"environment"`
	VpcId       string            `json:"vpcId"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	ExtraTags   map[string]string `json:"extraTags,omitempty"`
}
type CreateSecurityGroupOutput struct {
	SgId string `json:"sgId"`
}

type AddSecurityGroupRuleInput struct {
	StackName string            `json:"stackName"`
	SgId      string            `json:"sgId"`
	Direction string            `json:"direction"` // "ingress" or "egress"
	Rule      SecurityGroupRule `json:"rule"`
}

// SecurityGroupInput is the top-level input for SecurityGroupWorkflow.
type SecurityGroupInput struct {
	StackName    string              `json:"stackName"`
	Environment  string              `json:"environment"`
	VpcId        string              `json:"vpcId"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	IngressRules []SecurityGroupRule `json:"ingressRules,omitempty"`
	EgressRules  []SecurityGroupRule `json:"egressRules,omitempty"`
	ExtraTags    map[string]string   `json:"extraTags,omitempty"`
}
type SecurityGroupOutput struct {
	SgId string `json:"sgId"`
}

func (a *InfraActivities) CreateSecurityGroup(ctx context.Context, input CreateSecurityGroupInput) (CreateSecurityGroupOutput, error) {
	result, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		tags := envTags(input.Environment, input.ExtraTags)
		sg, err := ec2.NewSecurityGroup(pctx, "sg", &ec2.SecurityGroupArgs{
			VpcId:       pulumi.String(input.VpcId),
			Description: pulumi.String(input.Description),
			Ingress:     ec2.SecurityGroupIngressArray{},
			Egress:      ec2.SecurityGroupEgressArray{},
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

func (a *InfraActivities) AddSecurityGroupRule(ctx context.Context, input AddSecurityGroupRuleInput) error {
	_, err := a.upStack(ctx, input.StackName, func(pctx *pulumi.Context) error {
		cidrs := make(pulumi.StringArray, len(input.Rule.CidrBlocks))
		for i, c := range input.Rule.CidrBlocks {
			cidrs[i] = pulumi.String(c)
		}
		_, err := ec2.NewSecurityGroupRule(pctx, "sg-rule", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String(input.Direction),
			SecurityGroupId: pulumi.String(input.SgId),
			Protocol:        pulumi.String(input.Rule.Protocol),
			FromPort:        pulumi.Int(input.Rule.FromPort),
			ToPort:          pulumi.Int(input.Rule.ToPort),
			CidrBlocks:      cidrs,
		})
		return err
	})
	return err
}
