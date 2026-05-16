package workflows

import (
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// VpnWorkflow runs pulumi preview then pulumi up for the Client VPN stack.
// The TGW stack must be complete (cross-VPC routes in place) before calling this.
func VpnWorkflow(ctx workflow.Context, input activities.VpnInput) (activities.VpnOutputs, error) {
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

	if err := workflow.ExecuteActivity(previewCtx, acts.PreviewVpn, input).Get(previewCtx, nil); err != nil {
		return activities.VpnOutputs{}, err
	}

	var result activities.VpnOutputs
	if err := workflow.ExecuteActivity(upCtx, acts.UpVpn, input).Get(upCtx, &result); err != nil {
		return activities.VpnOutputs{}, err
	}
	return result, nil
}
