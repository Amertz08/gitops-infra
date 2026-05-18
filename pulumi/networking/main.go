package main

import (
	"fmt"

	"github.com/adammertz/gitops-infra/pulumi/networking/pkg/eks"
	"github.com/adammertz/gitops-infra/pulumi/networking/pkg/tgw"
	"github.com/adammertz/gitops-infra/pulumi/networking/pkg/vpc"
	"github.com/adammertz/gitops-infra/pulumi/networking/pkg/vpn"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type stackCfg struct {
	VpcCidr            string
	AvailabilityZones  []string
	PublicSubnetCidrs  []string
	PrivateSubnetCidrs []string
	EksInstanceType    string
	EksNodeCount       int
	VpnClientCidr      string // ops only
	OpsStackRef        string // spoke stacks only — format: <org>/networking/ops
}

var configs = map[string]stackCfg{
	"ops": {
		VpcCidr:            "10.0.0.0/16",
		AvailabilityZones:  []string{"us-east-1a", "us-east-1b"},
		PublicSubnetCidrs:  []string{"10.0.128.0/24", "10.0.129.0/24"},
		PrivateSubnetCidrs: []string{"10.0.0.0/20", "10.0.16.0/20"},
		EksInstanceType:    "m5.large",
		EksNodeCount:       3,
		VpnClientCidr:      "172.16.0.0/22",
	},
	"qa": {
		VpcCidr:            "10.1.0.0/16",
		AvailabilityZones:  []string{"us-east-1a", "us-east-1b"},
		PublicSubnetCidrs:  []string{"10.1.128.0/24", "10.1.129.0/24"},
		PrivateSubnetCidrs: []string{"10.1.0.0/20", "10.1.16.0/20"},
		EksInstanceType:    "m5.large",
		EksNodeCount:       2,
		OpsStackRef:        "Amertz08/networking/ops",
	},
	"prod": {
		VpcCidr:            "10.2.0.0/16",
		AvailabilityZones:  []string{"us-east-1a", "us-east-1b"},
		PublicSubnetCidrs:  []string{"10.2.128.0/24", "10.2.129.0/24"},
		PrivateSubnetCidrs: []string{"10.2.0.0/20", "10.2.16.0/20"},
		EksInstanceType:    "m5.xlarge",
		EksNodeCount:       3,
		OpsStackRef:        "Amertz08/networking/ops",
	},
}

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		c, ok := configs[ctx.Stack()]
		if !ok {
			return fmt.Errorf("no config defined for stack %q", ctx.Stack())
		}
		switch ctx.Stack() {
		case "ops":
			return deployOps(ctx, c)
		case "qa", "prod":
			return deploySpoke(ctx, c)
		default:
			return fmt.Errorf("unknown stack: %s", ctx.Stack())
		}
	})
}

func deployOps(ctx *pulumi.Context, c stackCfg) error {
	cfg := config.New(ctx, "networking")

	vpcOut, err := vpc.New(ctx, vpc.Args{
		Env:                "ops",
		CidrBlock:          c.VpcCidr,
		AvailabilityZones:  c.AvailabilityZones,
		PublicSubnetCidrs:  c.PublicSubnetCidrs,
		PrivateSubnetCidrs: c.PrivateSubnetCidrs,
	})
	if err != nil {
		return err
	}

	eksOut, err := eks.New(ctx, eks.Args{
		Env:              "ops",
		VpcId:            vpcOut.VpcId,
		PrivateSubnetIds: vpcOut.PrivateSubnetIds,
		InstanceType:     c.EksInstanceType,
		NodeCount:        c.EksNodeCount,
		PublicEndpoint:   true,
	})
	if err != nil {
		return err
	}

	tgwOut, err := tgw.NewTransitGateway(ctx, "ops")
	if err != nil {
		return err
	}

	err = tgw.NewVpcAttachment(ctx, "ops-tgw-attach", tgw.VpcAttachmentArgs{
		Env:                  "ops",
		TgwId:                tgwOut.TgwId,
		VpcId:                vpcOut.VpcId,
		PrivateSubnetIds:     vpcOut.PrivateSubnetIds,
		PrivateRouteTableIds: vpcOut.PrivateRouteTableIds,
		DestinationCidrs:     []string{configs["qa"].VpcCidr, configs["prod"].VpcCidr},
	})
	if err != nil {
		return err
	}

	vpnOut, err := vpn.New(ctx, vpn.Args{
		Env:              "ops",
		VpcId:            vpcOut.VpcId,
		PrivateSubnetIds: vpcOut.PrivateSubnetIds,
		ServerCertArn:    cfg.RequireSecret("vpnServerCertArn"),
		ClientCaArn:      cfg.RequireSecret("vpnClientCaArn"),
		ClientCidr:       c.VpnClientCidr,
		AuthorizedCidr:   "10.0.0.0/8",
		SpokeVpcCidrs:    []string{configs["qa"].VpcCidr, configs["prod"].VpcCidr},
	})
	if err != nil {
		return err
	}

	ctx.Export("vpcId", vpcOut.VpcId)
	ctx.Export("vpcCidr", pulumi.String(c.VpcCidr))
	ctx.Export("publicSubnetIds", vpcOut.PublicSubnetIds)
	ctx.Export("privateSubnetIds", vpcOut.PrivateSubnetIds)
	ctx.Export("clusterName", eksOut.ClusterName)
	ctx.Export("clusterEndpoint", eksOut.ClusterEndpoint)
	ctx.Export("clusterCertificateAuthority", eksOut.ClusterCertificateAuthority)
	ctx.Export("oidcProviderArn", eksOut.OidcProviderArn)
	ctx.Export("transitGatewayId", tgwOut.TgwId)
	ctx.Export("vpnEndpointId", vpnOut.EndpointId)
	ctx.Export("kubeconfig", pulumi.ToSecret(eksOut.Kubeconfig))

	return nil
}

func deploySpoke(ctx *pulumi.Context, c stackCfg) error {
	env := ctx.Stack()

	vpcOut, err := vpc.New(ctx, vpc.Args{
		Env:                env,
		CidrBlock:          c.VpcCidr,
		AvailabilityZones:  c.AvailabilityZones,
		PublicSubnetCidrs:  c.PublicSubnetCidrs,
		PrivateSubnetCidrs: c.PrivateSubnetCidrs,
	})
	if err != nil {
		return err
	}

	eksOut, err := eks.New(ctx, eks.Args{
		Env:              env,
		VpcId:            vpcOut.VpcId,
		PrivateSubnetIds: vpcOut.PrivateSubnetIds,
		InstanceType:     c.EksInstanceType,
		NodeCount:        c.EksNodeCount,
		PublicEndpoint:   false,
	})
	if err != nil {
		return err
	}

	opsRef, err := pulumi.NewStackReference(ctx, "ops-stack", &pulumi.StackReferenceArgs{
		Name: pulumi.String(c.OpsStackRef),
	})
	if err != nil {
		return err
	}

	tgwId := opsRef.GetOutput(pulumi.String("transitGatewayId")).
		ApplyT(func(v interface{}) string { return v.(string) }).(pulumi.StringOutput)

	// Routes: ops VPC + other spoke VPC + VPN client CIDR.
	destCidrs := map[string][]string{
		"qa":   {configs["ops"].VpcCidr, configs["prod"].VpcCidr, configs["ops"].VpnClientCidr},
		"prod": {configs["ops"].VpcCidr, configs["qa"].VpcCidr, configs["ops"].VpnClientCidr},
	}

	err = tgw.NewVpcAttachment(ctx, fmt.Sprintf("%s-tgw-attach", env), tgw.VpcAttachmentArgs{
		Env:                  env,
		TgwId:                tgwId,
		VpcId:                vpcOut.VpcId,
		PrivateSubnetIds:     vpcOut.PrivateSubnetIds,
		PrivateRouteTableIds: vpcOut.PrivateRouteTableIds,
		DestinationCidrs:     destCidrs[env],
	})
	if err != nil {
		return err
	}

	ctx.Export("vpcId", vpcOut.VpcId)
	ctx.Export("vpcCidr", pulumi.String(c.VpcCidr))
	ctx.Export("publicSubnetIds", vpcOut.PublicSubnetIds)
	ctx.Export("privateSubnetIds", vpcOut.PrivateSubnetIds)
	ctx.Export("clusterName", eksOut.ClusterName)
	ctx.Export("clusterEndpoint", eksOut.ClusterEndpoint)
	ctx.Export("clusterCertificateAuthority", eksOut.ClusterCertificateAuthority)
	ctx.Export("oidcProviderArn", eksOut.OidcProviderArn)

	return nil
}
