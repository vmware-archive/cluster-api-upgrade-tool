module github.com/vmware/cluster-api-upgrade-tool

go 1.12

require (
	github.com/blang/semver v3.5.0+incompatible
	github.com/go-logr/logr v0.1.0
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.2.0
	github.com/spf13/cobra v0.0.3
	github.com/stretchr/testify v1.4.0
	k8s.io/api v0.0.0-20190918195907-bd6ac527cfd2
	k8s.io/apimachinery v0.0.0-20190817020851-f2f3a405f61d
	k8s.io/client-go v0.0.0-20190918200256-06eb1244587a
	sigs.k8s.io/cluster-api v0.2.6
	sigs.k8s.io/cluster-api-bootstrap-provider-kubeadm v0.1.4
	sigs.k8s.io/controller-runtime v0.3.0
	sigs.k8s.io/yaml v1.1.0
)
