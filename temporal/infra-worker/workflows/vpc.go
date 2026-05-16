package workflows

import (
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// VpcWorkflow runs pulumi preview then pulumi up for a single VPC stack.
func VpcWorkflow(ctx workflow.Context, input activities.VpcInput) (activities.VpcOutputs, error) {
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

	if err := workflow.ExecuteActivity(previewCtx, acts.PreviewVpc, input).Get(previewCtx, nil); err != nil {
		return activities.VpcOutputs{}, err
	}

	var result activities.VpcOutputs
	if err := workflow.ExecuteActivity(upCtx, acts.UpVpc, input).Get(upCtx, &result); err != nil {
		return activities.VpcOutputs{}, err
	}
	return result, nil
}
