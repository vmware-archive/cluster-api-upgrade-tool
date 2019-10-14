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

Build: Run `make bin` from the root directory of this project.

Run `bin/cluster-api-upgrade-tool` against an existing cluster.

The following examples assume you have `$KUBECONFIG` set.


### Control plane upgrade
```
./bin/cluster-api-upgrade-tool \
  --cluster-namespace <Target cluster namespace> \
  --cluster-name <Target cluster name> \
  --kubernetes-version <Desired kubernetes version> \
  --scope control-plane
```

### Worker upgrade - all MachineDeployments
```
./bin/cluster-api-upgrade-tool \
  --cluster-namespace <Target cluster namespace> \
  --cluster-name <Target cluster name> \
  --kubernetes-version <Desired kubernetes version> \
  --scope machine-deployment
```

### Worker upgrade - single MachineDeployment, by name
```
./bin/cluster-api-upgrade-tool \
  --cluster-namespace <Target cluster namespace> \
  --cluster-name <Target cluster name> \
  --kubernetes-version <Desired kubernetes version> \
  --scope machine-deployment \
  --machine-deployment-name <Desired MachineDeployment name>
```

### Worker upgrade - multiple MachineDeployments, by label selector
```
./bin/cluster-api-upgrade-tool \
  --cluster-namespace <Target cluster namespace> \
  --cluster-name <Target cluster name> \
  --kubernetes-version <Desired kubernetes version> \
  --scope machine-deployment \
  --machine-deployment-selector <Desired MachineDeployment label selector>
```

### Prerequisites

* Cluster created using Cluster API v0.2.x / API version v1alpha2
* Nodes bootstrapped with kubeadm
* Control plane Machine resources have the following labels:
  * `cluster.k8s.io/cluster-name=<cluster name>`
  * `cluster.x-k8s.io/control-plane=true`
* Control plane is comprised of individual Machines
* Worker nodes are from MachineDeployments

## Documentation

### Usage

```
Usage:
  ./bin/cluster-api-upgrade-tool [flags]

Flags:
      --cluster-name string                  The name of target cluster (required)
      --cluster-namespace string             The namespace of target cluster (required)
  -h, --help                                 help for ./bin/cluster-api-upgrade-tool
      --image-field string                   The image identifier field in provider manifests (optional)
      --image-id string                      The provider-specific image identifier to use when booting a machine (optional)
      --kubeconfig string                    The kubeconfig path for the management cluster
      --kubernetes-version string            Desired kubernetes version to upgrade to (required)
      --machine-deployment-name string       Name of a single machine deployment to upgrade
      --machine-deployment-selector string   Label selector used to find machine deployments to upgrade
      --scope string                         Scope of upgrade - [control-plane | machine-deployment] (required)
      --upgrade-id string                    Unique identifier used to resume a partial upgrade (optional)
```

## Contributing

The cluster-api-upgrade-tool project team welcomes contributions from the community. If you wish to contribute code and you have not signed our contributor license agreement (CLA), our bot will update the issue when you open a Pull Request. For any questions about the CLA process, please refer to our [FAQ](https://cla.vmware.com/faq).

## License
Apache 2.0
