package certs

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/acm"
	"github.com/pulumi/pulumi-tls/sdk/v5/go/tls"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type Outputs struct {
	ServerCertArn pulumi.StringOutput
	ClientCaArn   pulumi.StringOutput
}

// New generates a self-signed CA and a server certificate signed by that CA,
// imports both into ACM, and returns their ARNs for use with the Client VPN endpoint.
// Private keys are marked sensitive by the TLS provider and encrypted in Pulumi state.
func New(ctx *pulumi.Context, env string, opts ...pulumi.ResourceOption) (*Outputs, error) {
	// CA private key
	caKey, err := tls.NewPrivateKey(ctx, fmt.Sprintf("%s-vpn-ca-key", env), &tls.PrivateKeyArgs{
		Algorithm: pulumi.String("RSA"),
		RsaBits:   pulumi.Int(2048),
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Self-signed CA certificate
	caCert, err := tls.NewSelfSignedCert(ctx, fmt.Sprintf("%s-vpn-ca-cert", env), &tls.SelfSignedCertArgs{
		PrivateKeyPem:      caKey.PrivateKeyPem,
		IsCaCertificate:    pulumi.Bool(true),
		ValidityPeriodHours: pulumi.Int(87600), // 10 years
		AllowedUses: pulumi.StringArray{
			pulumi.String("cert_signing"),
			pulumi.String("crl_signing"),
		},
		Subject: &tls.SelfSignedCertSubjectArgs{
			CommonName: pulumi.String(fmt.Sprintf("%s-vpn-ca", env)),
		},
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Server private key
	serverKey, err := tls.NewPrivateKey(ctx, fmt.Sprintf("%s-vpn-server-key", env), &tls.PrivateKeyArgs{
		Algorithm: pulumi.String("RSA"),
		RsaBits:   pulumi.Int(2048),
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Server CSR
	serverCSR, err := tls.NewCertRequest(ctx, fmt.Sprintf("%s-vpn-server-csr", env), &tls.CertRequestArgs{
		PrivateKeyPem: serverKey.PrivateKeyPem,
		Subject: &tls.CertRequestSubjectArgs{
			CommonName: pulumi.String(fmt.Sprintf("%s-vpn-server", env)),
		},
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Server certificate signed by CA
	serverCert, err := tls.NewLocallySignedCert(ctx, fmt.Sprintf("%s-vpn-server-cert", env), &tls.LocallySignedCertArgs{
		CertRequestPem:      serverCSR.CertRequestPem,
		CaPrivateKeyPem:     caKey.PrivateKeyPem,
		CaCertPem:           caCert.CertPem,
		ValidityPeriodHours: pulumi.Int(87600),
		AllowedUses: pulumi.StringArray{
			pulumi.String("digital_signature"),
			pulumi.String("key_encipherment"),
			pulumi.String("server_auth"),
		},
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Import server cert into ACM
	serverAcm, err := acm.NewCertificate(ctx, fmt.Sprintf("%s-vpn-server-acm", env), &acm.CertificateArgs{
		PrivateKey:       serverKey.PrivateKeyPem,
		CertificateBody:  serverCert.CertPem,
		CertificateChain: caCert.CertPem,
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Import CA cert into ACM (used as RootCertificateChainArn for client auth)
	caAcm, err := acm.NewCertificate(ctx, fmt.Sprintf("%s-vpn-ca-acm", env), &acm.CertificateArgs{
		PrivateKey:      caKey.PrivateKeyPem,
		CertificateBody: caCert.CertPem,
	}, opts...)
	if err != nil {
		return nil, err
	}

	return &Outputs{
		ServerCertArn: serverAcm.Arn,
		ClientCaArn:   caAcm.Arn,
	}, nil
}
