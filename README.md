# cluster-api-upgrade-tool

## Overview

This is a standalone tool to orchestrate upgrading the control plane machines of Kubernetes clusters created by Cluster
API v0.2.x / API version v1alpha2.

Our goal is to ultimately add this upgrade logic to Cluster API itself, but given that v1alpha2 doesn't easily lend
itself to handling upgrades of control plane machines, we decided to build a temporarily tool that can fill that gap.
Once Cluster API supports full lifecycle management of control planes, we plan to sunset this tool.

We have tested this with [Cluster API Provider for AWS](http://github.com/kubernetes-sigs/cluster-api-provider-aws)
(CAPA), and [Cluster API Provider for Docker](http://github.com/kubernetes-sigs/cluster-api-provider-docker) (CAPD).

## Warning!

While we've made every effort to build a robust tool that can perform upgrades correctly, including resuming a
partially-completed or interrupted upgrade, there is a chance that things might go awry. Therefore:

**BACK UP** your control plane before you attempt an upgrade. This may include:

- `Cluster` resource
- `Machine` resources
- `KubeadmConfig` resources
- `Secret` resources containing the certificates and kubeconfig for your cluster
- Taking a full snapshot of the workload cluster's etcd database
- Taking a full backup of the workload cluster using a Kubernetes backup tool, such as [Velero](https://velero.io)

## Try it out

Build: Run `make bin` from the root directory of this project.

Run `bin/cluster-api-upgrade-tool` against an existing cluster.

The following examples assume you have `$KUBECONFIG` set.


### Specify options with flags

```
./bin/cluster-api-upgrade-tool \
  --cluster-namespace <Target cluster namespace> \
  --cluster-name <Target cluster name> \
  --kubernetes-version <Desired kubernetes version> \
  --upgrade-id <desired upgrade id, such as a timestamp or some other unique identifier>
```

### Specify options with a config file

Given the following `my-file` config file:

```yaml
targetCluster:
  namespace: example
  name: cluster1
kubernetesVersion: v1.15.3
upgradeID: 1234
patches:
  infrastructure: '[{ "op": "replace", "path": "/spec/ami/id", "value": "ami-123456789" }]'
```

Run an upgrade with the following command:

```
./bin/cluster-api-upgrade-tool --config my-file
```

Both JSON and YAML formats are supported.

### Prerequisites

* Cluster created using Cluster API v0.2.x / API version v1alpha2
* Nodes bootstrapped with kubeadm
* Control plane Machine resources have the following labels:
  * `cluster.k8s.io/cluster-name=<cluster name>`
  * `cluster.x-k8s.io/control-plane=true`
* Control plane is comprised of individual Machines

## Documentation

### Usage

```
Usage:
  cluster-api-upgrade-tool [flags]

Flags:
      --bootstrap-patches string           JSON patch expression of patches to apply to the machine's bootstrap resource (optional)
      --cluster-name string                The name of target cluster (required)
      --cluster-namespace string           The namespace of target cluster (required)
      --config string                      Path to a config file in yaml or json format
  -h, --help                               help for cluster-api-upgrade-tool
      --infrastructure-patches string      JSON patch expression of patches to apply to the machine's infrastructure resource (optional)
      --kubeadm-configmap-patches string   JSON patch expression of patches to apply to the kubeadm-config ConfigMap (optional)
      --kubeconfig string                  The kubeconfig path for the management cluster
      --kubernetes-version string          Desired kubernetes version to upgrade to (required)
      --machine-timeout duration           How long to wait for machine operations (create, delete) to complete (default 15m0s)
      --upgrade-id string                  Unique identifier used to resume a partial upgrade (required)
```

### Patches

The tool supports using [JSON Patch](https://tools.ietf.org/html/rfc6902) to modify fields in the infrastructure and
bootstrap resources. An example use for this could be to change the image ID in an infrastructure provider's
machine resource. For example, to change the AMI for an `AWSMachine` for the AWS provider, you would use the following:

```
[{ "op": "replace", "path": "/spec/ami/id", "value": "ami-123456789" }]
```

When specifying patches, make sure you do not replace an entire field such as `metadata` or `spec` unless you include
the full content for the field. For example, if you want to add a label while retaining the existing ones, do **not**
use this patch, as it will replace the entire `metadata` field, potentially losing critical information:

```
[{ "op": "add", "path": "/metadata", "value": {"labels":{ "hello":"world"}} }]
```

Instead, do this:

```
[{ "op": "add", "path": "/metadata/labels/hello", "value": "world" }]
```

This requires that there are already labels present in the original resource. If not, you need to add the entire
labels field:

```
[{ "op": "add", "path": "/metadata/labels", "value": {"hello":"world"} }]
```

## Contributing

The cluster-api-upgrade-tool project team welcomes contributions from the community. If you wish to contribute code and
you have not signed our contributor license agreement (CLA), our bot will update the issue when you open a Pull Request.
For any questions about the CLA process, please refer to our [FAQ](https://cla.vmware.com/faq).

## License

Apache 2.0
