package reacter

import (
	"fmt"
	"net"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/ghetzel/go-stockutil/fileutil"
	"github.com/ghetzel/go-stockutil/log"
	"github.com/ghetzel/go-stockutil/netutil"
	"github.com/ghetzel/go-stockutil/pathutil"
	"github.com/ghetzel/go-stockutil/sliceutil"
	"github.com/ghetzel/go-stockutil/stringutil"
)

var defaultRegion = `us-east-1`
var defaultSharedCredentialsFile = `~/.aws/credentials`
var defaultSharedCredentialsProfile = sliceutil.OrString(
	os.Getenv(`AWS_PROFILE`),
	`default`,
)

func awsclient(region string, akid string, secret string, sessionToken string) (client.ConfigProvider, *aws.Config) {
	var providers []credentials.Provider

	if akid != `` && secret != `` {
		// if specified, explcitly-stated credentials override all
		providers = append(providers, &credentials.StaticProvider{
			Value: credentials.Value{
				AccessKeyID:     akid,
				SecretAccessKey: secret,
				SessionToken:    sessionToken,
			},
		})
	}

	// if the shared credentials file exists, use it
	if fileutil.FileExists(defaultSharedCredentialsFile) {
		if shared, err := pathutil.ExpandUser(defaultSharedCredentialsFile); err == nil {
			providers = append(providers, &credentials.SharedCredentialsProvider{
				Filename: shared,
				Profile:  defaultSharedCredentialsProfile,
			})
		} else {
			log.Warningf("Failed to expand path %v: %v", defaultSharedCredentialsFile, err)
		}
	}

	providers = append(providers, &credentials.EnvProvider{})

	return session.New(), &aws.Config{
		Region: aws.String(
			sliceutil.OrString(region, defaultRegion),
		),
		Credentials: credentials.NewChainCredentials(providers),
	}
}

func DiscoverEC2ByTag(tagName string, values ...string) ([]*netutil.Service, error) {
	session, config := awsclient(os.Getenv(`AWS_REGION`), ``, ``, ``)
	ec2Client := ec2.New(session, config)
	services := make([]*netutil.Service, 0)

	conf := &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String(`instance-state-name`),
				Values: aws.StringSlice([]string{`running`}),
			},
		},
	}

	if tagValues := sliceutil.CompactString(values); len(tagValues) == 0 {
		conf.Filters = append(conf.Filters, &ec2.Filter{
			Name:   aws.String(`tag-key`),
			Values: aws.StringSlice([]string{tagName}),
		})
	} else {
		conf.Filters = append(conf.Filters, &ec2.Filter{
			Name:   aws.String(fmt.Sprintf("tag:%s", tagName)),
			Values: aws.StringSlice(tagValues),
		})
	}

	if err := ec2Client.DescribeInstancesPages(conf, func(out *ec2.DescribeInstancesOutput, lastPage bool) bool {
		for _, resv := range out.Reservations {
			for _, instance := range resv.Instances {
				addresses := make([]net.IP, 0)

				if p := net.ParseIP(aws.StringValue(instance.PrivateIpAddress)); p != nil {
					addresses = append(addresses, p)
				}

				if p := net.ParseIP(aws.StringValue(instance.PublicIpAddress)); p != nil {
					addresses = append(addresses, p)
				}

				_, domain := stringutil.SplitPairTrailing(aws.StringValue(instance.PrivateDnsName), `.`)
				svc, tld := stringutil.SplitPairRight(domain, `.`)

				services = append(services, &netutil.Service{
					Hostname:  aws.StringValue(instance.PrivateDnsName),
					Service:   svc,
					Domain:    fmt.Sprintf(".%s", tld),
					Instance:  aws.StringValue(instance.InstanceId),
					Address:   addresses[0].String(),
					Addresses: addresses,
				})
			}
		}

		return !lastPage
	}); err == nil {
		return services, nil
	} else {
		return nil, err
	}
}
