package validate

import "testing"

func TestValidators(t *testing.T) {
	good := []error{
		InstanceID("i-0123456789abcdef0"), InstanceID("mi-0123456789abcdef0"),
		Region("eu-west-1"), Region("us-gov-east-1"),
		ClusterName("prod_cluster-1"), Profile("my-acct+role@x.y"),
		StartURL("https://d-123.awsapps.com/start"),
	}
	for i, err := range good {
		if err != nil {
			t.Errorf("good[%d]: %v", i, err)
		}
	}
	bad := []error{
		InstanceID("i-1&calc"), InstanceID("i-0123456789abcdef0 & calc"), InstanceID(""),
		Region("eu-west-1 & calc"), Region("--profile"), Region(""),
		ClusterName("-x"), ClusterName("a b"), ClusterName("a&b"),
		Profile("a&b"), Profile("a b"), Profile("a%PATH%"),
		StartURL("http://x.com"), StartURL("javascript:alert(1)"), StartURL("https://"), StartURL("file:///c:/x"),
	}
	for i, err := range bad {
		if err == nil {
			t.Errorf("bad[%d] accepted", i)
		}
	}
}
