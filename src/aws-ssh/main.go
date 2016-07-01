package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
)

var (
	allowedEnvironments = []string{}
	allowedRoles        = []string{}

	env       = flag.String("e", "dev", fmt.Sprintf("environment - one of: %s", strings.Join(allowedEnvironments, ", ")))
	role      = flag.String("r", "", fmt.Sprintf("role - one of: %s", strings.Join(allowedRoles, ", ")))
	suffix    = flag.String("s", "", "string to suffix to host name")
	region    = flag.String("region", "us-east-1", "AWS region")
	skipRoles = flag.String("skiproles", "nat", "roles to skip")
)

func usage() {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Outputs fragments of SSH config file of AWS instances\n\n")
	fmt.Fprintf(&buf, "usage: %s [options]\n\n", filepath.Base(os.Args[0]))
	fmt.Fprintf(&buf, "options:\n")
	fmt.Fprintf(&buf, "-e           environment\n")
	fmt.Fprintf(&buf, "-r           role\n")
	fmt.Fprintf(&buf, "-s           suffix to append to host name\n")
	fmt.Fprintf(&buf, "-region      AWS region (default: us-east-1)\n")
	fmt.Fprintf(&buf, "-skiproles   comma-delimited roles to skip (default: nat)\n")
	io.Copy(os.Stderr, &buf)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix(filepath.Base(os.Args[0]) + ": ")

	flag.Usage = usage
	flag.Parse()

	instances, err := getInstances(*region, *env, "bastion", nil)
	if err != nil {
		panic(err)
	}

	if len(instances) != 1 {
		log.Fatalf("expected 1 bastion host instance in the %s environment, found %d", *env, len(instances))
	}
	bastionHost := instances[0]

	rolesToSkip := strings.Split(*skipRoles, ",")

	instances, err = getInstances(*region, *env, *role, rolesToSkip)
	if err != nil {
		panic(err)
	}

	bastionTmpl := template.Must(template.New("bastion").Parse(bastion))
	if err := bastionTmpl.Execute(os.Stdout, struct {
		Host          string
		PublicDnsName string
		PathToKey     string
		User          string
	}{
		bastionHost.name,
		bastionHost.publicDnsName,
		"~/.ssh/" + bastionHost.keyName + ".pem",
		"ec2-user",
	}); err != nil {
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
			inst.name + *suffix,
			"~/.ssh/" + inst.keyName + ".pem",
			inst.privateIpAddr,
			"ec2-user",
			bastionHost.name,
		}); err != nil {
			panic(err)
		}
	}
}

var bastion = `
Host {{.Host}}
    Hostname {{.PublicDnsName}}
    IdentityFile {{.PathToKey}}
    ForwardAgent yes
    User {{.User}}
    StrictHostKeyChecking no
`

var host = `
Host {{.Host}}
    IdentityFile {{.PathToKey}}
    Hostname {{.PrivateIpAddr}}
    User {{.User}}
    StrictHostKeyChecking no
    ProxyCommand ssh {{.User}}@{{.BastionHost}} -W %h:%p
`

type instance struct {
	id            string
	name          string
	publicDnsName string
	privateIpAddr string
	keyName       string
}

func getInstances(region string, env string, role string, rolesToSkip []string) ([]instance, error) {
	svc := ec2.New(&aws.Config{Region: aws.String(region)})

	params := &ec2.DescribeInstancesInput{}
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
			if *inst.State.Name != "running" {
				continue
			}
			var name string
			for _, tag := range inst.Tags {
				if *tag.Key == "role" {
					role := *tag.Value
					if in(role, rolesToSkip) {
						continue
					}
				}
				if *tag.Key == "Name" {
					name = *tag.Value
				}
			}
			if name == "" {
				name = *inst.InstanceId
			}
			if role == "bastion" {
				name = "bastion-" + env
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

func in(needle string, haystack []string) bool {
	for i := range haystack {
		if needle == haystack[i] {
			return true
		}
	}
	return false
}
