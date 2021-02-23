module github.com/o2lab/race-checker

go 1.13

replace github.tamu.edu/April1989/go_tools v0.0.0 => ../go_tools

require (
	github.com/boltdb/bolt v1.3.1
	github.com/coreos/etcd v3.3.25+incompatible // indirect
	github.com/cznic/mathutil v0.0.0-20181122101859-297441e03548 // indirect
	github.com/cznic/sortutil v0.0.0-20181122101858-f5f958428db8 // indirect
	github.com/envoyproxy/go-control-plane v0.9.9-0.20210115003313-31f9241a16e6
	github.com/golang/protobuf v1.4.3
	github.com/grpc-ecosystem/go-grpc-middleware v1.2.2 // indirect
	github.com/juju/errors v0.0.0-20200330140219-3fe23663418f // indirect
	github.com/logrusorgru/aurora v0.0.0-20200102142835-e9ef32dff381
	github.com/ngaut/pools v0.0.0-20180318154953-b7bc8c42aac7 // indirect
	github.com/ngaut/sync2 v0.0.0-20141008032647-7a24ed77b2ef // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/pingcap/goleveldb v0.0.0-20191226122134-f82aafb29989 // indirect
	github.com/pingcap/kvproto v0.0.0-20210112051026-540f4cb76c1c // indirect
	github.com/pingcap/log v0.0.0-20201112100606-8f1e84a3abc8 // indirect
	github.com/pingcap/parser v3.1.2+incompatible // indirect
	github.com/pingcap/tidb v2.0.11+incompatible // indirect
	github.com/pingcap/tipb v0.0.0-20210112083036-a8cb6dfd13a1 // indirect
	github.com/prometheus/client_golang v1.9.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20200410134404-eec4a21b6bb0 // indirect
	github.com/rogpeppe/go-internal v1.6.2
	github.com/sirupsen/logrus v1.7.0
	github.com/spaolacci/murmur3 v1.1.0 // indirect
	github.com/twinj/uuid v1.0.0 // indirect
	github.com/twmb/algoimpl v0.0.0-20170717182524-076353e90b94
	github.com/uber/jaeger-client-go v2.25.0+incompatible // indirect
	github.com/uber/jaeger-lib v2.4.0+incompatible // indirect
	github.com/urfave/cli v1.22.5
	github.tamu.edu/April1989/go_tools v0.0.0
	go.uber.org/zap v1.16.0 // indirect
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a
	golang.org/x/sys v0.0.0-20201221093633-bc327ba9c2f0
	golang.org/x/tools v0.0.0-20210106214847-113979e3529a
	google.golang.org/grpc v1.34.0
	istio.io/istio v0.0.0-20210129100137-fc1367f5ecdb
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v11.0.0+incompatible
)
