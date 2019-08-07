module github.com/vmware/cluster-api-upgrade-tool

go 1.12

require (
	github.com/aws/aws-sdk-go v1.20.19 // indirect
	github.com/blang/semver v3.5.0+incompatible
	github.com/docker/spdystream v0.0.0-20181023171402-6480d4af844c // indirect
	github.com/elazarl/goproxy v0.0.0-20190711103511-473e67f1d7d2 // indirect
	github.com/elazarl/goproxy/ext v0.0.0-20190711103511-473e67f1d7d2 // indirect
	github.com/evanphx/json-patch v4.2.0+incompatible
	github.com/go-logr/logr v0.1.0
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.3.0
	github.com/spf13/cobra v0.0.3
	github.com/stretchr/testify v1.3.0
	k8s.io/api v0.0.0-20190704094930-781da4e7b28a
	k8s.io/apimachinery v0.0.0-20190704094625-facf06a8f4b8
	k8s.io/client-go v10.0.0+incompatible
	k8s.io/cluster-bootstrap v0.0.0-20190704110328-86aca54c9e62 // indirect
	k8s.io/kubernetes v1.13.8 // indirect
	sigs.k8s.io/cluster-api v0.1.9
	sigs.k8s.io/cluster-api-provider-aws v0.3.7
	sigs.k8s.io/yaml v1.1.0
)
