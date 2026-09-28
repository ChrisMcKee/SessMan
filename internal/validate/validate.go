// Package validate checks user/frontend-supplied identifiers before they are
// used in AWS calls or passed to external commands.
package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	instanceIDRe  = regexp.MustCompile(`^(i|mi)-[0-9a-f]{8,32}$`)
	regionRe      = regexp.MustCompile(`^[a-z]{2,4}(-[a-z]+)+-[0-9]{1,2}$`)
	clusterNameRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_-]{0,99}$`)
	profileRe     = regexp.MustCompile(`^[A-Za-z0-9+=,.@_-]{1,128}$`)
)

// InstanceID validates an EC2 (i-…) or managed (mi-…) instance id.
func InstanceID(s string) error {
	if !instanceIDRe.MatchString(s) {
		return fmt.Errorf("invalid instance id %q", s)
	}
	return nil
}

// Region validates an AWS region name such as eu-west-1.
func Region(s string) error {
	if !regionRe.MatchString(s) {
		return fmt.Errorf("invalid region %q", s)
	}
	return nil
}

// ClusterName validates an EKS cluster name.
func ClusterName(s string) error {
	if !clusterNameRe.MatchString(s) {
		return fmt.Errorf("invalid cluster name %q", s)
	}
	return nil
}

// Profile validates an AWS shared-config profile name.
func Profile(s string) error {
	if !profileRe.MatchString(s) {
		return fmt.Errorf("invalid profile name %q", s)
	}
	return nil
}

// StartURL requires an https URL with a host (IAM Identity Center portal).
func StartURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("start URL must be a valid https URL")
	}
	return nil
}

// SSORegion validates the Identity Center region.
func SSORegion(s string) error {
	if err := Region(strings.TrimSpace(s)); err != nil {
		return fmt.Errorf("invalid SSO region %q", s)
	}
	return nil
}
