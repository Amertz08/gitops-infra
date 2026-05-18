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

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		switch ctx.Stack() {
		case "ops":
			return deployOps(ctx)
		case "qa":
			return deploySpoke(ctx, "qa")
		case "prod":
			return deploySpoke(ctx, "prod")
		default:
			return fmt.Errorf("unknown stack: %s", ctx.Stack())
		}
	})
}

func deployOps(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "networking")

	var azs, publicCidrs, privateCidrs []string
	cfg.RequireObject("availabilityZones", &azs)
	cfg.RequireObject("publicSubnetCidrs", &publicCidrs)
	cfg.RequireObject("privateSubnetCidrs", &privateCidrs)

	vpcOut, err := vpc.New(ctx, vpc.Args{
		Env:                "ops",
		CidrBlock:          cfg.Require("vpcCidr"),
		AvailabilityZones:  azs,
		PublicSubnetCidrs:  publicCidrs,
		PrivateSubnetCidrs: privateCidrs,
	})
	if err != nil {
		return err
	}

	eksOut, err := eks.New(ctx, eks.Args{
		Env:              "ops",
		VpcId:            vpcOut.VpcId,
		PrivateSubnetIds: vpcOut.PrivateSubnetIds,
		InstanceType:     cfg.Require("eksInstanceType"),
		NodeCount:        cfg.RequireInt("eksNodeCount"),
		PublicEndpoint:   true,
	})
	if err != nil {
		return err
	}

	tgwOut, err := tgw.NewTransitGateway(ctx, "ops")
	if err != nil {
		return err
	}

	// Attach the ops VPC and inject routes for spoke VPCs + VPN clients.
	err = tgw.NewVpcAttachment(ctx, "ops-tgw-attach", tgw.VpcAttachmentArgs{
		Env:                  "ops",
		TgwId:                tgwOut.TgwId,
		VpcId:                vpcOut.VpcId,
		PrivateSubnetIds:     vpcOut.PrivateSubnetIds,
		PrivateRouteTableIds: vpcOut.PrivateRouteTableIds,
		DestinationCidrs:     []string{"10.1.0.0/16", "10.2.0.0/16"},
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
		ClientCidr:       cfg.Require("vpnClientCidr"),
		AuthorizedCidr:   "10.0.0.0/8",
		SpokeVpcCidrs:    []string{"10.1.0.0/16", "10.2.0.0/16"},
	})
	if err != nil {
		return err
	}

	ctx.Export("vpcId", vpcOut.VpcId)
	ctx.Export("vpcCidr", pulumi.String(cfg.Require("vpcCidr")))
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

func deploySpoke(ctx *pulumi.Context, env string) error {
	cfg := config.New(ctx, "networking")

	var azs, publicCidrs, privateCidrs []string
	cfg.RequireObject("availabilityZones", &azs)
	cfg.RequireObject("publicSubnetCidrs", &publicCidrs)
	cfg.RequireObject("privateSubnetCidrs", &privateCidrs)

	vpcOut, err := vpc.New(ctx, vpc.Args{
		Env:                env,
		CidrBlock:          cfg.Require("vpcCidr"),
		AvailabilityZones:  azs,
		PublicSubnetCidrs:  publicCidrs,
		PrivateSubnetCidrs: privateCidrs,
	})
	if err != nil {
		return err
	}

	eksOut, err := eks.New(ctx, eks.Args{
		Env:              env,
		VpcId:            vpcOut.VpcId,
		PrivateSubnetIds: vpcOut.PrivateSubnetIds,
		InstanceType:     cfg.Require("eksInstanceType"),
		NodeCount:        cfg.RequireInt("eksNodeCount"),
		PublicEndpoint:   false,
	})
	if err != nil {
		return err
	}

	// Read TGW ID and ops CIDR from the ops stack via StackReference.
	// Update networking:opsStackRef in Pulumi.<env>.yaml to match your Pulumi org:
	//   format: <org>/networking/ops  (run `pulumi whoami` to get the org)
	opsRef, err := pulumi.NewStackReference(ctx, "ops-stack", &pulumi.StackReferenceArgs{
		Name: pulumi.String(cfg.Require("opsStackRef")),
	})
	if err != nil {
		return err
	}

	tgwId := opsRef.GetOutput(pulumi.String("transitGatewayId")).
		ApplyT(func(v interface{}) string { return v.(string) }).(pulumi.StringOutput)

	// Destination CIDRs for this spoke: ops VPC + other spoke VPC + VPN client CIDR.
	destCidrs := map[string][]string{
		"qa":   {"10.0.0.0/16", "10.2.0.0/16", "172.16.0.0/22"},
		"prod": {"10.0.0.0/16", "10.1.0.0/16", "172.16.0.0/22"},
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
	ctx.Export("vpcCidr", pulumi.String(cfg.Require("vpcCidr")))
	ctx.Export("publicSubnetIds", vpcOut.PublicSubnetIds)
	ctx.Export("privateSubnetIds", vpcOut.PrivateSubnetIds)
	ctx.Export("clusterName", eksOut.ClusterName)
	ctx.Export("clusterEndpoint", eksOut.ClusterEndpoint)
	ctx.Export("clusterCertificateAuthority", eksOut.ClusterCertificateAuthority)
	ctx.Export("oidcProviderArn", eksOut.OidcProviderArn)

	return nil
}
