module github.com/vmware/cluster-api-upgrade-tool/test/integration

go 1.12

require (
	cloud.google.com/go v0.38.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.8.5 // indirect
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.4.2
	github.com/vmware/cluster-api-upgrade-tool v0.1.0
	google.golang.org/grpc v1.19.1 // indirect
	k8s.io/api v0.0.0-20190918195907-bd6ac527cfd2
	k8s.io/apimachinery v0.0.0-20190817020851-f2f3a405f61d
	k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	k8s.io/utils v0.0.0-20190809000727-6c36bc71fc4a
	sigs.k8s.io/cluster-api v0.2.6
	sigs.k8s.io/cluster-api-bootstrap-provider-kubeadm v0.1.4
	sigs.k8s.io/cluster-api-provider-docker v0.2.0
	sigs.k8s.io/controller-runtime v0.3.0
)

replace (
	github.com/vmware/cluster-api-upgrade-tool => ../..
	k8s.io/client-go => k8s.io/client-go v0.0.0-20190918200256-06eb1244587a
)
