package workflows

import (
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TgwWorkflow runs pulumi preview then pulumi up for the Transit Gateway stack.
// All three VPC stacks must be complete before calling this workflow.
func TgwWorkflow(ctx workflow.Context, input activities.TgwInput) (activities.TgwOutputs, error) {
	var acts *activities.InfraActivities

	previewCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	})
	upCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})

	if err := workflow.ExecuteActivity(previewCtx, acts.PreviewTgw, input).Get(previewCtx, nil); err != nil {
		return activities.TgwOutputs{}, err
	}

	var result activities.TgwOutputs
	if err := workflow.ExecuteActivity(upCtx, acts.UpTgw, input).Get(upCtx, &result); err != nil {
		return activities.TgwOutputs{}, err
	}
	return result, nil
}
