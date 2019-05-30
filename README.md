

# cluster-api-upgrade-tool

## Overview

Cluster-api-upgrade-tool is a standalone library to orchestrate cluster upgrades using the v1alpha1 API version from Cluster API.
Our goal is to ultimately add upgrade logic to Cluster API itself,
and this will be worked on as part of the control plane and node lifecycle management workstreams.

Note that cluster-api-upgrade-tool effort is still in the prototype stage. All of the code here is to experiment with the tool and demo its abilities, in order to drive more technical feedback.
Please do not use this against production clusters.


## Try it out

For the purpose of running this against an existing cluster,this project provides an experimental command line program to trigger the API's of this library

Build: Run "go build" from the home directory of this project.

Run the generated binary against an existing cluster.
Example:

./cluster-api-upgrade-tool --kubeconfig <Path to your management cluster kubeconfig file> \
--ca-field spec.providerSpec.value.caKeyPair \
--cluster-name <Name of your target cluster> \
--cluster-namespace <target cluster namespace> \
--kubernetes-version <New kubernetes version>

### Prerequisites

* Applicable for cluster created by cluster API

### Build & Run

go build

## Documentation

## Contributing

The cluster-api-upgrade-tool project team welcomes contributions from the community. Before you start working with cluster-api-upgrade-tool, please
read our [Developer Certificate of Origin](https://cla.vmware.com/dco). All contributions to this repository must be
signed as described on that page. Your signature certifies that you wrote the patch or have the right to pass it on
as an open-source patch. For more detailed information, refer to [CONTRIBUTING.md](CONTRIBUTING.md).

## License
Apache 2.0