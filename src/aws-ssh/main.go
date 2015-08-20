package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/template"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

var (
	allowedEnvironments = []string{}
	allowedRoles        = []string{}
)

func main() {
	var env = flag.String("e", "dev",
		fmt.Sprintf("environment - one of: %s", strings.Join(allowedEnvironments, ", ")))
	var role = flag.String("r", "",
		fmt.Sprintf("role - one of: %s", strings.Join(allowedRoles, ", ")))
	var region = flag.String("region", "us-east-1", "AWS region")

	flag.Parse()

	instances, err := getInstances(*region, *env, "bastion")
	if err != nil {
		panic(err)
	}

	if len(instances) != 1 {
		log.Fatalf("expected 1 bastion host instance in the %s environment, found %d", len(instances))
	}
	bastionHost := instances[0]

	instances, err = getInstances(*region, *env, *role)
	if err != nil {
		panic(err)
	}

	tmpl := template.Must(template.New("host").Parse(host))

	for _, inst := range instances {
		if err := tmpl.Execute(os.Stdout, struct {
			Host          string
			PathToKey     string
			PrivateIpAddr string
			User          string
			BastionHost   string
		}{
			inst.name,
			"~/.ssh/" + inst.keyName + ".pem",
			inst.privateIpAddr,
			"ec2-user",
			bastionHost.publicDnsName,
		}); err != nil {
			panic(err)
		}
	}
}

var host = `
Host {{.Host}}
    IdentityFile {{.PathToKey}}
    ForwardAgent yes
    Hostname {{.PrivateIpAddr}}
    User {{.User}}
    ProxyCommand ssh {{.User}}@{{.BastionHost}} -W %h:%p
`

type instance struct {
	id            string
	name          string
	publicDnsName string
	privateIpAddr string
	keyName       string
}

func getInstances(region string, env string, role string) ([]instance, error) {
	svc := ec2.New(&aws.Config{Region: aws.String(region)})

	params := new(ec2.DescribeInstancesInput)
	if env != "" {
		params.Filters = append(params.Filters, &ec2.Filter{
			Name:   aws.String("tag:env"),
			Values: []*string{aws.String(env)},
		})
	}
	if role != "" {
		params.Filters = append(params.Filters, &ec2.Filter{
			Name:   aws.String("tag:role"),
			Values: []*string{aws.String(role)},
		})
	}

	resp, err := svc.DescribeInstances(params)
	if err != nil {
		return nil, err
	}

	log.Printf("Found %d reservation(s)", len(resp.Reservations))

	var instances []instance

	for _, res := range resp.Reservations {
		log.Printf("Found %d instance(s) in the reservation", len(res.Instances))

		for _, inst := range res.Instances {
			var name string
			for _, tag := range inst.Tags {
				if *tag.Key == "Name" {
					name = *tag.Value
				}
			}
			if name == "" {
				name = *inst.InstanceId
			}

			instances = append(instances, instance{
				id:            *inst.InstanceId,
				name:          name,
				publicDnsName: *inst.PublicDnsName,
				privateIpAddr: *inst.PrivateIpAddress,
				keyName:       *inst.KeyName,
			})
		}
	}

	return instances, err
}
