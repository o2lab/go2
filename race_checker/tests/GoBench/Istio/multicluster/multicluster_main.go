// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"istio.io/istio/pilot/pkg/serviceregistry/aggregate"
	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/kube/secretcontroller"
	//pkgtest "istio.io/istio/pkg/test"

	"istio.io/istio/pilot/pkg/serviceregistry/kube/controller" //bz: i added
)

const (
	testSecretName      = "testSecretName"
	testSecretNameSpace = "istio-system"
	WatchedNamespaces   = "istio-system"
	DomainSuffix        = "fake_domain"
	ResyncPeriod        = 1 * time.Second
)

var mockserviceController = &aggregate.Controller{}

func createMultiClusterSecret(k8s kube.Client) error {
	data := map[string][]byte{}
	secret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testSecretNameSpace,
			Labels: map[string]string{
				secretcontroller.MultiClusterSecretLabel: "true",
			},
		},
		Data: map[string][]byte{},
	}

	data["testRemoteCluster"] = []byte("Test")
	secret.Data = data
	_, err := k8s.CoreV1().Secrets(testSecretNameSpace).Create(context.TODO(), &secret, metav1.CreateOptions{})
	return err
}

func deleteMultiClusterSecret(k8s kube.Client) error {
	var immediate int64

	return k8s.CoreV1().Secrets(testSecretNameSpace).Delete(
		context.TODO(),
		testSecretName, metav1.DeleteOptions{GracePeriodSeconds: &immediate})
}

//func verifyControllers(t *testing.T, m *controller.Multicluster, expectedControllerCount int, timeoutName string) {
//	t.Helper()
//	pkgtest.NewEventualOpts(10*time.Millisecond, 5*time.Second).Eventually(t, timeoutName, func() bool {
//		//m.m.Lock()  //bz: i changed
//		//defer m.m.Unlock()
//		//return len(m.remoteKubeControllers) == expectedControllerCount
//		return true
//	})
//}

func Test_KubeSecretController(t *testing.T) {
	secretcontroller.BuildClientsFromConfig = func(kubeConfig []byte) (kube.Client, error) {
		return kube.NewFakeClient(), nil
	}
	clientset := kube.NewFakeClient()
	stop := make(chan struct{})
	//t.Cleanup(func() {
	//	close(stop)
	//})
	mc := controller.NewMulticluster(
		"pilot-abc-123",
		clientset,
		testSecretNameSpace,
		controller.Options{
			WatchedNamespaces: WatchedNamespaces,
			DomainSuffix:      DomainSuffix,
			ResyncPeriod:      ResyncPeriod,
			SyncInterval:      time.Microsecond,
		}, mockserviceController, nil, "", "default", nil, nil)
	mc.InitSecretController(stop)
	cache.WaitForCacheSync(stop, mc.HasSynced)
	clientset.RunAndWait(stop)

	// Create the multicluster secret. Sleep to allow created remote
	// controller to start and callback add function to be called.
	err := createMultiClusterSecret(clientset)
	if err != nil {
		t.Fatalf("Unexpected error on secret create: %v", err)
	}

	//// Test - Verify that the remote controller has been added.
	//verifyControllers(t, mc, 1, "create remote controller")

	// Delete the mulicluster secret.
	err = deleteMultiClusterSecret(clientset)
	if err != nil {
		t.Fatalf("Unexpected error on secret delete: %v", err)
	}

	//// Test - Verify that the remote controller has been removed.
	//verifyControllers(t, mc, 0, "delete remote controller")

}


func main() {
	var t *testing.T
	Test_KubeSecretController(t)
}