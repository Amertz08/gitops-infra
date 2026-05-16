package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		cfg := config.New(ctx, "")
		serverCertArn := cfg.Require("serverCertArn")
		clientCaArn := cfg.Require("clientCaArn")

		awsCfg := config.New(ctx, "aws")
		region := awsCfg.Get("region")
		if region == "" {
			region = "us-east-1"
		}
		azs := []string{region + "a", region + "b", region + "c"}

		opsVpc, err := NewVpc(ctx, "ops", &VpcArgs{
			CidrBlock:   "10.0.0.0/16",
			Azs:         azs,
			Environment: "ops",
		})
		if err != nil {
			return err
		}

		qaVpc, err := NewVpc(ctx, "qa", &VpcArgs{
			CidrBlock:   "10.1.0.0/16",
			Azs:         azs,
			Environment: "qa",
		})
		if err != nil {
			return err
		}

		prodVpc, err := NewVpc(ctx, "prod", &VpcArgs{
			CidrBlock:   "10.2.0.0/16",
			Azs:         azs,
			Environment: "prod",
		})
		if err != nil {
			return err
		}

		tgw, err := NewTransitGateway(ctx, "main", &TransitGatewayArgs{
			HubVpc:        opsVpc,
			SpokeVpcs:     []*VpcComponent{qaVpc, prodVpc},
			VpnClientCidr: "172.16.0.0/22",
		})
		if err != nil {
			return err
		}

		vpn, err := NewClientVpn(ctx, "main", &ClientVpnArgs{
			ServerCertArn: serverCertArn,
			ClientCaArn:   clientCaArn,
			OpsVpc:        opsVpc,
			SpokeVpcCidrs: []string{"10.1.0.0/16", "10.2.0.0/16"},
			ClientCidr:    "172.16.0.0/22",
		})
		if err != nil {
			return err
		}

		ctx.Export("opsVpcId", opsVpc.VpcId)
		ctx.Export("qaVpcId", qaVpc.VpcId)
		ctx.Export("prodVpcId", prodVpc.VpcId)
		ctx.Export("opsPrivateSubnetIds", opsVpc.PrivateSubnetIds)
		ctx.Export("qaPrivateSubnetIds", qaVpc.PrivateSubnetIds)
		ctx.Export("prodPrivateSubnetIds", prodVpc.PrivateSubnetIds)
		ctx.Export("transitGatewayId", tgw.TgwId)
		ctx.Export("clientVpnEndpointId", vpn.EndpointId)

		return nil
	})
}
