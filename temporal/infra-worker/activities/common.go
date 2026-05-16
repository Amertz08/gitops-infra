package activities

import (
	"context"
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"go.temporal.io/sdk/activity"
)

// AWSPluginVersion is the Pulumi AWS provider plugin version installed at worker
// startup. Should match the major version of pulumi-aws/sdk/v6 in go.mod.
const AWSPluginVersion = "v6.83.3"

// InfraActivities holds shared config for all infrastructure Pulumi activities.
// All stacks use ProjectName as their Pulumi project name; Region sets aws:region.
type InfraActivities struct {
	ProjectName string // e.g. "gitops-infra"
	Region      string // e.g. "us-east-1"
}

func (a *InfraActivities) configureStack(ctx context.Context, stack auto.Stack) error {
	return stack.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: a.Region})
}

// heartbeatWriter streams Pulumi output to Temporal heartbeats so the server
// can detect stuck activities and deliver cancellation signals.
type heartbeatWriter struct {
	ctx context.Context
}

func (w *heartbeatWriter) Write(p []byte) (int, error) {
	activity.RecordHeartbeat(w.ctx, string(p))
	return len(p), nil
}

// mergeTags merges extra tags into a base tag map (extra wins on conflict).
func mergeTags(base, extra pulumi.StringMap) pulumi.StringMap {
	result := pulumi.StringMap{}
	for k, v := range base {
		result[k] = v
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

// extractStringSlice converts a Pulumi array output value to []string.
func extractStringSlice(v auto.OutputValue) []string {
	raw, ok := v.Value.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, len(raw))
	for i, item := range raw {
		result[i] = fmt.Sprintf("%v", item)
	}
	return result
}
