package eksmgr

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"

	"sessman/internal/validate"
)

// maxDescribers bounds concurrent DescribeCluster calls.
const maxDescribers = 8

const (
	// AWSCLIInstallURL is the official AWS CLI install guide.
	AWSCLIInstallURL = "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
)

// Cluster is an EKS cluster summary.
type Cluster struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Version string `json:"version"`
	Arn     string `json:"arn"`
	// Error is set when the cluster could not be described.
	Error string `json:"error,omitempty"`
}

// SetupStatus reports local tooling required for kubeconfig updates.
type SetupStatus struct {
	AWSCLI    bool   `json:"awsCli"`
	Ready     bool   `json:"ready"`
	AWSCLIURL string `json:"awsCliUrl"`
}

// CheckSetup returns whether the AWS CLI is available.
func CheckSetup() SetupStatus {
	ok := AWSCLIInstalled()
	return SetupStatus{
		AWSCLI:    ok,
		Ready:     ok,
		AWSCLIURL: AWSCLIInstallURL,
	}
}

// AWSCLIInstalled reports whether the AWS CLI is on PATH.
func AWSCLIInstalled() bool {
	_, err := exec.LookPath("aws")
	return err == nil
}

// ListClusters lists EKS clusters in a region and describes each one.
func ListClusters(ctx context.Context, profile, region string) ([]Cluster, error) {
	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config for profile %q: %w", profile, err)
	}
	client := eks.NewFromConfig(cfg)

	var names []string
	var nextToken *string
	for {
		out, err := client.ListClusters(ctx, &eks.ListClustersInput{
			NextToken:  nextToken,
			MaxResults: aws.Int32(100),
		})
		if err != nil {
			return nil, fmt.Errorf("list clusters: %w", err)
		}
		names = append(names, out.Clusters...)
		if out.NextToken == nil || aws.ToString(out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	clusters := make([]Cluster, len(names))
	sem := make(chan struct{}, maxDescribers)
	var wg sync.WaitGroup
	for i, name := range names {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			desc, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(name)})
			if err != nil || desc.Cluster == nil {
				msg := "no cluster in response"
				if err != nil {
					msg = err.Error()
				}
				clusters[i] = Cluster{Name: name, Status: "UNKNOWN", Error: msg}
				return
			}
			c := desc.Cluster
			clusters[i] = Cluster{
				Name:    aws.ToString(c.Name),
				Status:  string(c.Status),
				Version: aws.ToString(c.Version),
				Arn:     aws.ToString(c.Arn),
			}
		}()
	}
	wg.Wait()
	return clusters, nil
}

// UpdateKubeconfig runs aws eks update-kubeconfig for the cluster.
func UpdateKubeconfig(profile, region, clusterName string) error {
	setup := CheckSetup()
	if !setup.Ready {
		return fmt.Errorf("AWS CLI not found on PATH; install it from %s", AWSCLIInstallURL)
	}
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return fmt.Errorf("cluster name is required")
	}
	for _, err := range []error{validate.ClusterName(clusterName), validate.Region(region), validate.Profile(profile)} {
		if err != nil {
			return err
		}
	}
	cmd := exec.Command(
		"aws", "eks", "update-kubeconfig",
		"--name", clusterName,
		"--region", region,
		"--profile", profile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("update-kubeconfig failed: %s", msg)
	}
	return nil
}
