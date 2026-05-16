package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
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

// upStack opens (or upserts) a Pulumi stack, configures it, and runs Up.
// A background ticker sends a keepalive heartbeat every 30 s so that Temporal
// does not false-timeout the activity during silent AWS provisioning waits.
func (a *InfraActivities) upStack(ctx context.Context, stackName string, program pulumi.RunFunc) (auto.UpResult, error) {
	stack, err := auto.UpsertStackInlineSource(ctx, stackName, a.ProjectName, program)
	if err != nil {
		return auto.UpResult{}, err
	}
	if err := a.configureStack(ctx, stack); err != nil {
		return auto.UpResult{}, err
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				activity.RecordHeartbeat(ctx, stackName)
			case <-stop:
				return
			}
		}
	}()
	result, err := stack.Up(ctx, optup.ProgressStreams(&heartbeatWriter{ctx: ctx}))
	close(stop)
	return result, err
}

// envTags builds the base tag map for resources in a given environment.
func envTags(env string, extra map[string]string) pulumi.StringMap {
	tags := pulumi.StringMap{
		"Environment": pulumi.String(env),
		"ManagedBy":   pulumi.String("Pulumi"),
	}
	for k, v := range extra {
		tags[k] = pulumi.String(v)
	}
	return tags
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
