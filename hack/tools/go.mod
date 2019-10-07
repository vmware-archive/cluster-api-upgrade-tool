module github.com/vmware/cluster-api-upgrade-tool/hack/tools

go 1.12

require sigs.k8s.io/cluster-api-provider-docker v0.1.3

replace (
	k8s.io/api => k8s.io/api v0.0.0-20181213150558-05914d821849
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20181127025237-2b1284ed4c93
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.0.0-20190704050804-bd8686edbd81
	k8s.io/client-go => k8s.io/client-go v10.0.0+incompatible
	k8s.io/kubernetes => k8s.io/kubernetes v1.13.1
)
