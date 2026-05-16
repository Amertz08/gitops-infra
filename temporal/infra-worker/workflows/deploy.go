package workflows

import (
	"github.com/adammertz/gitops-infra/temporal/infra-worker/activities"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// InfraInput is the top-level input for a full infrastructure deployment.
type InfraInput struct {
	Region        string `json:"region"`        // e.g. "us-east-1"
	ServerCertArn string `json:"serverCertArn"` // ACM ARN for VPN server cert
	ClientCaArn   string `json:"clientCaArn"`   // ACM ARN of the client CA cert
}

// InfraOutputs holds the aggregated outputs from all child workflows.
type InfraOutputs struct {
	OpsVpc  activities.VpcOutputs `json:"opsVpc"`
	QaVpc   activities.VpcOutputs `json:"qaVpc"`
	ProdVpc activities.VpcOutputs `json:"prodVpc"`
	Tgw     activities.TgwOutputs `json:"tgw"`
	Vpn     activities.VpnOutputs `json:"vpn"`
}

// InfraStatus is returned by the "status" query.
type InfraStatus struct {
	Phase    string          `json:"phase"`    // "vpc"|"tgw"|"vpn"|"done"|"failed"
	VpcReady map[string]bool `json:"vpcReady"` // {"ops": true, "qa": false, "prod": false}
}

// vpcChannelResult carries the result of one VPC child workflow through a channel.
type vpcChannelResult struct {
	Env     string                 `json:"env"`
	Outputs activities.VpcOutputs `json:"outputs"`
	ErrMsg  string                 `json:"errMsg,omitempty"`
}

// InfraDeployWorkflow orchestrates the full networking deployment:
//  1. Creates ops, qa, and prod VPCs in parallel (child workflows).
//  2. Creates the Transit Gateway connecting all three.
//  3. Creates the Client VPN endpoint in the ops VPC.
//
// Query "status" at any time to see the current phase.
func InfraDeployWorkflow(ctx workflow.Context, input InfraInput) (InfraOutputs, error) {
	logger := workflow.GetLogger(ctx)

	region := input.Region
	if region == "" {
		region = "us-east-1"
	}
	azs := []string{region + "a", region + "b", region + "c"}

	status := InfraStatus{Phase: "vpc", VpcReady: map[string]bool{
		"ops": false, "qa": false, "prod": false,
	}}
	if err := workflow.SetQueryHandler(ctx, "status", func() (InfraStatus, error) {
		return status, nil
	}); err != nil {
		return InfraOutputs{}, err
	}

	vpcConfigs := []activities.VpcInput{
		{
			StackName:          "ops-vpc",
			Environment:        "ops",
			CidrBlock:          "10.0.0.0/16",
			PublicSubnetCidrs:  []string{"10.0.128.0/24"},
			PrivateSubnetCidrs: []string{"10.0.0.0/20", "10.0.16.0/20", "10.0.32.0/20"},
			Azs:                azs,
		},
		{
			StackName:          "qa-vpc",
			Environment:        "qa",
			CidrBlock:          "10.1.0.0/16",
			PublicSubnetCidrs:  []string{"10.1.128.0/24"},
			PrivateSubnetCidrs: []string{"10.1.0.0/20", "10.1.16.0/20", "10.1.32.0/20"},
			Azs:                azs,
		},
		{
			StackName:          "prod-vpc",
			Environment:        "prod",
			CidrBlock:          "10.2.0.0/16",
			PublicSubnetCidrs:  []string{"10.2.128.0/24"},
			PrivateSubnetCidrs: []string{"10.2.0.0/20", "10.2.16.0/20", "10.2.32.0/20"},
			Azs:                azs,
		},
	}

	// Fan-out: run all three VPC child workflows concurrently.
	ch := workflow.NewChannel(ctx)
	cwo := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	for _, cfg := range vpcConfigs {
		cfg := cfg
		workflow.Go(ctx, func(gctx workflow.Context) {
			var out activities.VpcOutputs
			err := workflow.ExecuteChildWorkflow(cwo, VpcWorkflow, cfg).Get(gctx, &out)
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			ch.Send(gctx, vpcChannelResult{Env: cfg.Environment, Outputs: out, ErrMsg: errMsg})
		})
	}

	// Fan-in: collect all three VPC results before proceeding.
	vpcByEnv := make(map[string]activities.VpcOutputs, len(vpcConfigs))
	for range vpcConfigs {
		var r vpcChannelResult
		ch.Receive(ctx, &r)
		if r.ErrMsg != "" {
			status.Phase = "failed"
			return InfraOutputs{}, temporal.NewApplicationError(r.ErrMsg, "VpcDeployError")
		}
		vpcByEnv[r.Env] = r.Outputs
		status.VpcReady[r.Env] = true
		logger.Info("VPC ready", "env", r.Env, "vpcId", r.Outputs.VpcId)
	}

	// Transit Gateway — sequential after all VPCs.
	status.Phase = "tgw"
	tgwInput := activities.TgwInput{
		HubVpc:        vpcByEnv["ops"],
		SpokeVpcs:     []activities.VpcOutputs{vpcByEnv["qa"], vpcByEnv["prod"]},
		VpnClientCidr: "172.16.0.0/22",
	}
	var tgwOut activities.TgwOutputs
	if err := workflow.ExecuteChildWorkflow(cwo, TgwWorkflow, tgwInput).Get(ctx, &tgwOut); err != nil {
		status.Phase = "failed"
		return InfraOutputs{}, err
	}
	logger.Info("Transit Gateway ready", "tgwId", tgwOut.TgwId)

	// Client VPN — sequential after Transit Gateway.
	status.Phase = "vpn"
	opsVpc := vpcByEnv["ops"]
	vpnInput := activities.VpnInput{
		ServerCertArn:      input.ServerCertArn,
		ClientCaArn:        input.ClientCaArn,
		OpsVpcId:           opsVpc.VpcId,
		OpsPrivateSubnetId: opsVpc.PrivateSubnetIds[0],
		SpokeVpcCidrs:      []string{"10.1.0.0/16", "10.2.0.0/16"},
		ClientCidr:         "172.16.0.0/22",
	}
	var vpnOut activities.VpnOutputs
	if err := workflow.ExecuteChildWorkflow(cwo, VpnWorkflow, vpnInput).Get(ctx, &vpnOut); err != nil {
		status.Phase = "failed"
		return InfraOutputs{}, err
	}
	logger.Info("Client VPN ready", "endpointId", vpnOut.EndpointId)

	status.Phase = "done"
	return InfraOutputs{
		OpsVpc:  vpcByEnv["ops"],
		QaVpc:   vpcByEnv["qa"],
		ProdVpc: vpcByEnv["prod"],
		Tgw:     tgwOut,
		Vpn:     vpnOut,
	}, nil
}
