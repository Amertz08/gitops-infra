package activities

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"go.temporal.io/sdk/activity"
)

// StackInput identifies a Pulumi stack. Project maps to a directory under
// pulumi/ in the repo (e.g. "aws-networking"), StackName is the Pulumi stack
// (e.g. "main").
type StackInput struct {
	Project   string `json:"project"`
	StackName string `json:"stackName"`
}

// StackOutputs holds the exported key/value pairs from a successful pulumi up.
type StackOutputs struct {
	Outputs map[string]string `json:"outputs"`
}

// PulumiActivities wraps Pulumi Automation API operations as Temporal activities.
// RepoRoot must point to the root of the gitops-infra checkout; the Pulumi CLI
// must be on PATH of the worker process.
type PulumiActivities struct {
	RepoRoot string
}

func (a *PulumiActivities) Preview(ctx context.Context, input StackInput) error {
	stack, err := a.openStack(ctx, input)
	if err != nil {
		return err
	}
	w := &heartbeatWriter{ctx: ctx}
	_, err = stack.Preview(ctx, optpreview.ProgressStreams(w))
	return err
}

func (a *PulumiActivities) Up(ctx context.Context, input StackInput) (StackOutputs, error) {
	stack, err := a.openStack(ctx, input)
	if err != nil {
		return StackOutputs{}, err
	}
	w := &heartbeatWriter{ctx: ctx}
	result, err := stack.Up(ctx, optup.ProgressStreams(w))
	if err != nil {
		return StackOutputs{}, err
	}
	outputs := make(map[string]string, len(result.Outputs))
	for k, v := range result.Outputs {
		outputs[k] = fmt.Sprintf("%v", v.Value)
	}
	return StackOutputs{Outputs: outputs}, nil
}

func (a *PulumiActivities) Destroy(ctx context.Context, input StackInput) error {
	stack, err := a.openStack(ctx, input)
	if err != nil {
		return err
	}
	w := &heartbeatWriter{ctx: ctx}
	_, err = stack.Destroy(ctx, optdestroy.ProgressStreams(w))
	return err
}

func (a *PulumiActivities) openStack(ctx context.Context, input StackInput) (auto.Stack, error) {
	workDir := filepath.Join(a.RepoRoot, "pulumi", input.Project)
	return auto.UpsertStackLocalSource(ctx, input.StackName, workDir)
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
