package workflows

import (
	"time"

	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// DeployStatus is returned by the "status" query handler.
type DeployStatus struct {
	Phase   string            `json:"phase"`   // starting|previewing|deploying|done|failed
	Message string            `json:"message,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
}

// InfraDeployWorkflow runs pulumi preview then pulumi up for the given stack.
// Query "status" at any point to get the current phase and, once done, the
// stack outputs.
func InfraDeployWorkflow(ctx workflow.Context, input activities.StackInput) (activities.StackOutputs, error) {
	logger := workflow.GetLogger(ctx)

	status := DeployStatus{Phase: "starting"}
	if err := workflow.SetQueryHandler(ctx, "status", func() (DeployStatus, error) {
		return status, nil
	}); err != nil {
		return activities.StackOutputs{}, err
	}

	// Use a nil pointer to reference activity methods — Temporal resolves the
	// actual registered instance at execution time.
	var acts *activities.PulumiActivities

	previewCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	status.Phase = "previewing"
	logger.Info("Running pulumi preview", "project", input.Project, "stack", input.StackName)
	if err := workflow.ExecuteActivity(previewCtx, acts.Preview, input).Get(previewCtx, nil); err != nil {
		status.Phase = "failed"
		status.Message = err.Error()
		return activities.StackOutputs{}, err
	}

	upCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	})

	status.Phase = "deploying"
	logger.Info("Running pulumi up", "project", input.Project, "stack", input.StackName)
	var result activities.StackOutputs
	if err := workflow.ExecuteActivity(upCtx, acts.Up, input).Get(upCtx, &result); err != nil {
		status.Phase = "failed"
		status.Message = err.Error()
		return activities.StackOutputs{}, err
	}

	status.Phase = "done"
	status.Outputs = result.Outputs
	logger.Info("Deployment complete", "project", input.Project, "stack", input.StackName)
	return result, nil
}
