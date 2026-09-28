package ssmmgr

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"sessman/internal/validate"
)

const (
	// AWSCLIInstallURL is the official AWS CLI install guide.
	AWSCLIInstallURL = "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
	// PluginInstallURL is the Session Manager plugin install guide.
	PluginInstallURL = "https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html"
)

// Instance is an SSM-managed EC2 (or managed) instance.
type Instance struct {
	InstanceID   string `json:"instanceId"`
	Name         string `json:"name"`
	PingStatus   string `json:"pingStatus"`
	PlatformName string `json:"platformName"`
	PlatformType string `json:"platformType"`
	IPAddress    string `json:"ipAddress"`
}

// SetupStatus reports local tooling required for SSM connect.
type SetupStatus struct {
	AWSCLI    bool   `json:"awsCli"`
	Plugin    bool   `json:"plugin"`
	Ready     bool   `json:"ready"`
	AWSCLIURL string `json:"awsCliUrl"`
	PluginURL string `json:"pluginUrl"`
}

// CheckSetup returns whether AWS CLI and the Session Manager plugin are installed.
func CheckSetup() SetupStatus {
	awsOK := AWSCLIInstalled()
	pluginOK := PluginInstalled()
	return SetupStatus{
		AWSCLI:    awsOK,
		Plugin:    pluginOK,
		Ready:     awsOK && pluginOK,
		AWSCLIURL: AWSCLIInstallURL,
		PluginURL: PluginInstallURL,
	}
}

// ListOnline lists SSM instances with Online ping status for a named profile/region.
// Name prefers the EC2 Name tag when available.
func ListOnline(ctx context.Context, profile, region string) ([]Instance, error) {
	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithSharedConfigProfile(profile),
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config for profile %q: %w", profile, err)
	}
	client := ssm.NewFromConfig(cfg)

	var out []Instance
	var ids []string
	var nextToken *string
	for {
		resp, err := client.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
			MaxResults: aws.Int32(50),
			NextToken:  nextToken,
			Filters: []ssmtypes.InstanceInformationStringFilter{
				{
					Key:    aws.String("PingStatus"),
					Values: []string{string(ssmtypes.PingStatusOnline)},
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("describe instance information: %w", err)
		}
		for _, info := range resp.InstanceInformationList {
			id := aws.ToString(info.InstanceId)
			name := aws.ToString(info.ComputerName)
			if name == "" {
				name = id
			}
			out = append(out, Instance{
				InstanceID:   id,
				Name:         name,
				PingStatus:   string(info.PingStatus),
				PlatformName: aws.ToString(info.PlatformName),
				PlatformType: string(info.PlatformType),
				IPAddress:    aws.ToString(info.IPAddress),
			})
			if strings.HasPrefix(id, "i-") {
				ids = append(ids, id)
			}
		}
		if resp.NextToken == nil || aws.ToString(resp.NextToken) == "" {
			break
		}
		nextToken = resp.NextToken
	}

	if len(ids) > 0 {
		names, err := ec2NameTags(ctx, cfg, ids)
		if err == nil {
			for i := range out {
				if n, ok := names[out[i].InstanceID]; ok && n != "" {
					out[i].Name = n
				}
			}
		}
	}
	return out, nil
}

func ec2NameTags(ctx context.Context, cfg aws.Config, instanceIDs []string) (map[string]string, error) {
	client := ec2.NewFromConfig(cfg)
	names := map[string]string{}
	const batch = 100
	for i := 0; i < len(instanceIDs); i += batch {
		end := i + batch
		if end > len(instanceIDs) {
			end = len(instanceIDs)
		}
		chunk := instanceIDs[i:end]
		resp, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: chunk,
		})
		if err != nil {
			return nil, err
		}
		for _, res := range resp.Reservations {
			for _, inst := range res.Instances {
				id := aws.ToString(inst.InstanceId)
				if n := tagValue(inst.Tags, "Name"); n != "" {
					names[id] = n
				}
			}
		}
	}
	return names, nil
}

func tagValue(tags []ec2types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}

// PluginInstalled reports whether the Session Manager plugin is on PATH.
func PluginInstalled() bool {
	_, err := exec.LookPath("session-manager-plugin")
	return err == nil
}

// AWSCLIInstalled reports whether the AWS CLI is available.
func AWSCLIInstalled() bool {
	_, err := exec.LookPath("aws")
	return err == nil
}

// Connect opens an OS terminal running aws ssm start-session.
func Connect(profile, region, instanceID string) error {
	// These end up on a terminal command line, so reject anything that is not
	// a well-formed identifier before it can reach a shell.
	for _, err := range []error{validate.Profile(profile), validate.Region(region), validate.InstanceID(instanceID)} {
		if err != nil {
			return err
		}
	}
	setup := CheckSetup()
	if !setup.Ready {
		var missing []string
		if !setup.AWSCLI {
			missing = append(missing, "AWS CLI ("+AWSCLIInstallURL+")")
		}
		if !setup.Plugin {
			missing = append(missing, "Session Manager plugin ("+PluginInstallURL+")")
		}
		return fmt.Errorf("SSM setup incomplete — missing: %s", strings.Join(missing, "; "))
	}
	args := []string{
		"ssm", "start-session",
		"--target", instanceID,
		"--profile", profile,
		"--region", region,
	}
	return openTerminal("aws", args...)
}

func openTerminal(command string, args ...string) error {
	switch runtime.GOOS {
	case "windows":
		line := quoteWindows(command)
		for _, a := range args {
			line += " " + quoteWindows(a)
		}
		cmd := exec.Command("cmd", "/c", "start", "AWS SSM", "cmd", "/k", line)
		return cmd.Start()
	case "darwin":
		script := fmt.Sprintf(
			`tell application "Terminal" to do script %s`,
			appleScriptQuote(shellJoin(command, args...)),
		)
		cmd := exec.Command("osascript", "-e", script)
		return cmd.Start()
	default:
		full := shellJoin(command, args...)
		candidates := [][]string{
			{"x-terminal-emulator", "-e", "bash", "-lc", full + "; exec bash"},
			{"gnome-terminal", "--", "bash", "-lc", full + "; exec bash"},
			{"konsole", "-e", "bash", "-lc", full + "; exec bash"},
			{"xfce4-terminal", "-e", "bash -lc " + shellQuote(full+"; exec bash")},
		}
		for _, c := range candidates {
			if _, err := exec.LookPath(c[0]); err != nil {
				continue
			}
			cmd := exec.Command(c[0], c[1:]...)
			if err := cmd.Start(); err == nil {
				return nil
			}
		}
		return fmt.Errorf("no terminal emulator found to start SSM session")
	}
}

// quoteWindows only handles whitespace and quotes; callers must have validated
// the value (see validate) so cmd metacharacters (& | ^ % < >) cannot occur.
func quoteWindows(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func shellJoin(command string, args ...string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, shellQuote(command))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
