package main

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

func main() {
	client := fake.NewSimpleClientset()

	broadcaster := record.NewBroadcaster()
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{
		Component: "my-controller",
	})
	lock, err := resourcelock.New(
		resourcelock.EndpointsResourceLock,
		"default",
		"my-controller",
		client.CoreV1(),
		nil,
		resourcelock.ResourceLockConfig{
			Identity:      "host",
			EventRecorder: recorder,
		},
	)
	if err != nil {
		panic(err)
	}

	for i := 0; ; i++ {
		fmt.Println(i)
		ctx, cancel := context.WithCancel(context.Background())
		le, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
			Lock:          lock,
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
				},
				OnStoppedLeading: func() {
				},
			},
		})
		if err != nil {
			panic(err)
		}
		go func() {
			time.Sleep(20 * time.Microsecond)
			cancel()
		}()
		le.Run(ctx)
	}
}
