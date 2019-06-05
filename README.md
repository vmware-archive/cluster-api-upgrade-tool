# cluster-api-upgrade-tool

## WARNING

This tool is a work in progress. It may have bugs. **DO NOT** use it on a production Kubernetes cluster or any cluster you can't live without.

To date, we have only tested this with [Cluster API Provider for AWS](http://github.com/kubernetes-sigs/cluster-api-provider-aws) (CAPA)

## Overview

This is a standalone tool to orchestrate upgrading Kubernetes clusters created by Cluster API v0.1.x / API version v1alpha1.

Our goal is to ultimately add this upgrade logic to Cluster API itself, but given that v1alpha1 doesn't easily lend itself to
handling upgrades of control plane machines, we decided to build a temporarily tool that can fill that gap. Once Cluster API
supports full lifecycle management of both control planes and worker nodes, we plan to deprecate this tool.

## Try it out

Build: Run "go build" from the home directory of this project.

Run the generated binary against an existing cluster.
Example:

````
./cluster-api-upgrade-tool --kubeconfig <Path to your management cluster kubeconfig file> \
  --ca-field spec.providerSpec.value.caKeyPair \
  --cluster-namespace <target cluster namespace> \
  --cluster-name <Name of your target cluster> \
  --kubernetes-version <Desired kubernetes version>
````

### Prerequisites

* Cluster created using Cluster API v0.1.x / API version v1alpha1
* Nodes bootstrapped with kubeadm
* Control plane Machine resources have the following labels:
  * `cluster.k8s.io/cluster-name=<cluster name>`
  * `set=controlplane`
* Control plane is comprised of individual Machines
* Worker nodes are from MachineDeployments

### Build & Run

go build

## Documentation

TODO

## Contributing

The cluster-api-upgrade-tool project team welcomes contributions from the community. If you wish to contribute code and you have not signed our contributor license agreement (CLA), our bot will update the issue when you open a Pull Request. For any questions about the CLA process, please refer to our [FAQ](https://cla.vmware.com/faq).

## License
Apache 2.0
