package main

import (
	"io/ioutil"
	v3 "istio.io/istio/pilot/pkg/xds/v3"
	"istio.io/istio/pkg/test/env"
	"path/filepath"
	"strings"
	"testing"

	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	hcm "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/envoyproxy/go-control-plane/pkg/wellknown"
	"github.com/golang/protobuf/ptypes"

	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/xds"
	"istio.io/istio/pkg/config/labels"
)

var watchAll = []string{v3.ClusterType, v3.EndpointType, v3.ListenerType, v3.RouteType}

func mustReadfolder(t *testing.T, folder string) string {
	result := ""
	fpathRoot := folder
	if !strings.HasPrefix(fpathRoot, ".") {
		fpathRoot = filepath.Join(env.IstioSrc, folder)
	}
	f, err := ioutil.ReadDir(fpathRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, fpath := range f {
		bytes, err := ioutil.ReadFile(filepath.Join(fpathRoot, fpath.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result += "---\n"
		result += string(bytes)
	}
	return result
}

// TestLDS using default sidecar in root namespace
func TestLDSEnvoyFilterWithWorkloadSelector(t *testing.T) {
	s := xds.NewFakeDiscoveryServer(t, xds.FakeOptions{
		ConfigString: mustReadfolder(t, "tests/testdata/networking/envoyfilter-without-service"),
	})
	// The labels of 98.1.1.1 must match the envoyfilter workload selector
	s.MemRegistry.AddWorkload("98.1.1.1", labels.Instance{"app": "envoyfilter-test-app", "some": "otherlabel"})
	s.MemRegistry.AddWorkload("98.1.1.2", labels.Instance{"app": "no-envoyfilter-test-app"})
	s.MemRegistry.AddWorkload("98.1.1.3", labels.Instance{})

	tests := []struct {
		name            string
		ip              string
		expectLuaFilter bool
	}{
		{
			name:            "Add filter with matching labels to sidecar",
			ip:              "98.1.1.1",
			expectLuaFilter: true,
		},
		{
			name:            "Ignore filter with not matching labels to sidecar",
			ip:              "98.1.1.2",
			expectLuaFilter: false,
		},
		{
			name:            "Ignore filter with empty labels to sidecar",
			ip:              "98.1.1.3",
			expectLuaFilter: false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			adsc := s.Connect(&model.Proxy{ConfigNamespace: "consumerns", IPAddresses: []string{test.ip}}, nil, watchAll)

			// Expect 1 HTTP listeners for 8081, 1 hybrid listeners for 15006 (virtual inbound)
			if len(adsc.GetHTTPListeners()) != 2 {
				t.Fatalf("Expected 2 http listeners, got %d", len(adsc.GetHTTPListeners()))
			}
			// TODO: This is flimsy. The ADSC code treats any listener with http connection manager as a HTTP listener
			// instead of looking at it as a listener with multiple filter chains
			l := adsc.GetHTTPListeners()["0.0.0.0_8081"]

			expectLuaFilter(t, l, test.expectLuaFilter)
		})
	}
}

func expectLuaFilter(t *testing.T, l *listener.Listener, expected bool) {
	t.Helper()
	if l != nil {
		var chain *listener.FilterChain
		for _, fc := range l.FilterChains {
			if len(fc.Filters) == 1 && fc.Filters[0].Name == wellknown.HTTPConnectionManager {
				chain = fc
			}
		}
		if chain == nil {
			t.Fatalf("Failed to find http_connection_manager")
		}
		if len(chain.Filters) != 1 {
			t.Fatalf("Expected 1 filter in first filter chain, got %d", len(l.FilterChains))
		}
		filter := chain.Filters[0]
		if filter.Name != wellknown.HTTPConnectionManager {
			t.Fatalf("Expected HTTP connection, found %v", chain.Filters[0].Name)
		}
		httpCfg, ok := filter.ConfigType.(*listener.Filter_TypedConfig)
		if !ok {
			t.Fatalf("Expected Http Connection Manager Config Filter_TypedConfig, found %T", filter.ConfigType)
		}
		connectionManagerCfg := hcm.HttpConnectionManager{}
		err := ptypes.UnmarshalAny(httpCfg.TypedConfig, &connectionManagerCfg)
		if err != nil {
			t.Fatalf("Could not deserialize http connection manager config: %v", err)
		}
		found := false
		for _, filter := range connectionManagerCfg.HttpFilters {
			if filter.Name == "envoy.lua" {
				found = true
			}
		}
		if expected != found {
			t.Fatalf("Expected Lua filter: %v, found: %v", expected, found)
		}
	}
}


func main() {
	var t *testing.T
	TestLDSEnvoyFilterWithWorkloadSelector(t)
}
