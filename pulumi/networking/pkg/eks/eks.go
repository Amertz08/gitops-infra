package eks

import (
	"encoding/json"
	"fmt"

	awseks "github.com/pulumi/pulumi-aws/sdk/v6/go/aws/eks"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type Args struct {
	Env              string
	VpcId            pulumi.StringInput
	PrivateSubnetIds pulumi.StringArrayInput
	InstanceType     string
	NodeCount        int
	// PublicEndpoint exposes the Kubernetes API server publicly.
	// Set true for the ops/mgmt cluster during initial bootstrap; false for qa/prod.
	PublicEndpoint bool
}

type Outputs struct {
	ClusterName                 pulumi.StringOutput
	ClusterEndpoint             pulumi.StringOutput
	ClusterCertificateAuthority pulumi.StringOutput
	OidcProviderArn             pulumi.StringOutput
	// Kubeconfig uses aws eks get-token for authentication (no static credentials).
	Kubeconfig pulumi.StringOutput
}

func New(ctx *pulumi.Context, args Args, opts ...pulumi.ResourceOption) (*Outputs, error) {
	env := args.Env

	// Cluster IAM role
	clusterRoleDoc, err := json.Marshal(map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":    "Allow",
				"Principal": map[string]string{"Service": "eks.amazonaws.com"},
				"Action":    "sts:AssumeRole",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	clusterRole, err := iam.NewRole(ctx, fmt.Sprintf("%s-cluster-role", env), &iam.RoleArgs{
		AssumeRolePolicy: pulumi.String(string(clusterRoleDoc)),
		Tags:             resourceTags(env, fmt.Sprintf("%s-cluster-role", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	_, err = iam.NewRolePolicyAttachment(ctx, fmt.Sprintf("%s-cluster-policy", env), &iam.RolePolicyAttachmentArgs{
		Role:      clusterRole.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"),
	}, opts...)
	if err != nil {
		return nil, err
	}

	// EKS cluster
	cluster, err := awseks.NewCluster(ctx, fmt.Sprintf("%s-cluster", env), &awseks.ClusterArgs{
		RoleArn: clusterRole.Arn,
		VpcConfig: &awseks.ClusterVpcConfigArgs{
			SubnetIds:             args.PrivateSubnetIds,
			EndpointPrivateAccess: pulumi.Bool(true),
			EndpointPublicAccess:  pulumi.Bool(args.PublicEndpoint),
		},
		Tags: resourceTags(env, fmt.Sprintf("%s-cluster", env)),
	}, append(opts, pulumi.DependsOn([]pulumi.Resource{clusterRole}))...)
	if err != nil {
		return nil, err
	}

	// OIDC provider — thumbprint is the stable AWS intermediate CA for EKS OIDC endpoints.
	oidcIssuer := cluster.Identities.Index(pulumi.Int(0)).Oidcs().Index(pulumi.Int(0)).Issuer().Elem()

	oidcProvider, err := iam.NewOpenIdConnectProvider(ctx, fmt.Sprintf("%s-oidc", env), &iam.OpenIdConnectProviderArgs{
		Url:             oidcIssuer,
		ClientIdLists:   pulumi.StringArray{pulumi.String("sts.amazonaws.com")},
		ThumbprintLists: pulumi.StringArray{pulumi.String("9e99a48a9960b14926bb7f3b02e22da2b0ab7280")},
		Tags:            resourceTags(env, fmt.Sprintf("%s-oidc", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	// Node group IAM role
	nodeRoleDoc, err := json.Marshal(map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect":    "Allow",
				"Principal": map[string]string{"Service": "ec2.amazonaws.com"},
				"Action":    "sts:AssumeRole",
			},
		},
	})
	if err != nil {
		return nil, err
	}

	nodeRole, err := iam.NewRole(ctx, fmt.Sprintf("%s-node-role", env), &iam.RoleArgs{
		AssumeRolePolicy: pulumi.String(string(nodeRoleDoc)),
		Tags:             resourceTags(env, fmt.Sprintf("%s-node-role", env)),
	}, opts...)
	if err != nil {
		return nil, err
	}

	nodePolicies := []string{
		"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
		"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
		"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
	}
	for i, policy := range nodePolicies {
		_, err = iam.NewRolePolicyAttachment(ctx, fmt.Sprintf("%s-node-policy-%d", env, i), &iam.RolePolicyAttachmentArgs{
			Role:      nodeRole.Name,
			PolicyArn: pulumi.String(policy),
		}, opts...)
		if err != nil {
			return nil, err
		}
	}

	// Managed node group
	_, err = awseks.NewNodeGroup(ctx, fmt.Sprintf("%s-nodes", env), &awseks.NodeGroupArgs{
		ClusterName:   cluster.Name,
		NodeRoleArn:   nodeRole.Arn,
		SubnetIds:     args.PrivateSubnetIds,
		InstanceTypes: pulumi.StringArray{pulumi.String(args.InstanceType)},
		ScalingConfig: &awseks.NodeGroupScalingConfigArgs{
			DesiredSize: pulumi.Int(args.NodeCount),
			MinSize:     pulumi.Int(1),
			MaxSize:     pulumi.Int(args.NodeCount + 2),
		},
		Tags: resourceTags(env, fmt.Sprintf("%s-nodes", env)),
	}, append(opts, pulumi.DependsOn([]pulumi.Resource{cluster, nodeRole}))...)
	if err != nil {
		return nil, err
	}

	// Kubeconfig with aws CLI token auth (no static credentials stored)
	kubeconfig := pulumi.All(cluster.Name, cluster.Endpoint, cluster.CertificateAuthority.Data().Elem()).
		ApplyT(func(vals []interface{}) (string, error) {
			name     := vals[0].(string)
			endpoint := vals[1].(string)
			ca       := vals[2].(string)
			return fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: %s
    server: %s
  name: %s
contexts:
- context:
    cluster: %s
    user: admin
  name: %s
current-context: %s
kind: Config
preferences: {}
users:
- name: admin
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args:
      - eks
      - get-token
      - --cluster-name
      - %s
`, ca, endpoint, name, name, name, name, name), nil
		}).(pulumi.StringOutput)

	return &Outputs{
		ClusterName:                 cluster.Name,
		ClusterEndpoint:             cluster.Endpoint,
		ClusterCertificateAuthority: cluster.CertificateAuthority.Data().Elem(),
		OidcProviderArn:             oidcProvider.Arn,
		Kubeconfig:                  kubeconfig,
	}, nil
}

func resourceTags(env, name string) pulumi.StringMap {
	return pulumi.StringMap{
		"Environment": pulumi.String(env),
		"ManagedBy":   pulumi.String("Pulumi"),
		"Name":        pulumi.String(name),
	}
}
