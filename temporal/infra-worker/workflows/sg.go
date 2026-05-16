package workflows

import (
	"fmt"
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// SecurityGroupWorkflow creates a security group and adds all ingress/egress rules:
//
//	Step 1: CreateSecurityGroup
//	Step 2: AddSecurityGroupRule×N (ingress) | AddSecurityGroupRule×N (egress) — all parallel
func SecurityGroupWorkflow(ctx workflow.Context, input activities.SecurityGroupInput) (activities.SecurityGroupOutput, error) {
	var acts *activities.InfraActivities

	opts := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	var sgOut activities.CreateSecurityGroupOutput
	if err := workflow.ExecuteActivity(opts, acts.CreateSecurityGroup, activities.CreateSecurityGroupInput{
		StackName:   input.StackName,
		Environment: input.Environment,
		VpcId:       input.VpcId,
		Name:        input.Name,
		Description: input.Description,
		ExtraTags:   input.ExtraTags,
	}).Get(ctx, &sgOut); err != nil {
		return activities.SecurityGroupOutput{}, err
	}

	var futures []workflow.Future
	for i, rule := range input.IngressRules {
		futures = append(futures, workflow.ExecuteActivity(opts, acts.AddSecurityGroupRule, activities.AddSecurityGroupRuleInput{
			StackName: fmt.Sprintf("%s-ingress-%d", input.StackName, i),
			SgId:      sgOut.SgId,
			Direction: "ingress",
			Rule:      rule,
		}))
	}
	for i, rule := range input.EgressRules {
		futures = append(futures, workflow.ExecuteActivity(opts, acts.AddSecurityGroupRule, activities.AddSecurityGroupRuleInput{
			StackName: fmt.Sprintf("%s-egress-%d", input.StackName, i),
			SgId:      sgOut.SgId,
			Direction: "egress",
			Rule:      rule,
		}))
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return activities.SecurityGroupOutput{}, err
		}
	}

	return activities.SecurityGroupOutput{SgId: sgOut.SgId}, nil
}
