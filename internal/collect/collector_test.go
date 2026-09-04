package collect

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/delangetimm/beholdr/internal/k8s"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSource implements Source with canned data and injectable per-call
// errors, so collect() failure/recovery paths can be exercised without a
// real cluster.
type fakeSource struct {
	nodes       []corev1.Node
	pods        []corev1.Pod
	deployments []appsv1.Deployment
	hpas        []autoscalingv1.HorizontalPodAutoscaler
	nodeUsage   map[string]k8s.Usage
	podUsage    map[string]k8s.Usage

	nodesErr error
	podsErr  error
	depsErr  error
}

func (f *fakeSource) Nodes(context.Context) ([]corev1.Node, error) { return f.nodes, f.nodesErr }
func (f *fakeSource) Pods(context.Context) ([]corev1.Pod, error)   { return f.pods, f.podsErr }
func (f *fakeSource) Deployments(context.Context) ([]appsv1.Deployment, error) {
	return f.deployments, f.depsErr
}
func (f *fakeSource) HPAs(context.Context) ([]autoscalingv1.HorizontalPodAutoscaler, error) {
	return f.hpas, nil
}
func (f *fakeSource) NodeMetrics(context.Context) map[string]k8s.Usage { return f.nodeUsage }
func (f *fakeSource) PodMetrics(context.Context) map[string]k8s.Usage  { return f.podUsage }

func qty(s string) resource.Quantity { return resource.MustParse(s) }

func int32Ptr(v int32) *int32 { return &v }

// oneNodeOnePodFixture is a minimal, internally-consistent cluster: one ready
// node running one pod owned by a Deployment.
func oneNodeOnePodFixture() *fakeSource {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    qty("4"),
				corev1.ResourceMemory: qty("8Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    qty("3800m"),
				corev1.ResourceMemory: qty("7500Mi"),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.30.0"},
		},
	}
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "web-abc123xy",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-abc123xy"}},
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    qty("100m"),
						corev1.ResourceMemory: qty("128Mi"),
					},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	dep := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(1)},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	}
	return &fakeSource{
		nodes:       []corev1.Node{node},
		pods:        []corev1.Pod{pod},
		deployments: []appsv1.Deployment{dep},
		nodeUsage:   map[string]k8s.Usage{"node-1": {CPUMilli: 500, MemBytes: 1 << 30}},
		podUsage:    map[string]k8s.Usage{"default/web-abc123xy": {CPUMilli: 50, MemBytes: 64 << 20}},
	}
}

func TestCollectSuccessUpdatesSnapshotAndHealth(t *testing.T) {
	src := oneNodeOnePodFixture()
	c := New(src, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())

	c.collect(context.Background())

	snap := c.Snapshot()
	if !snap.Ready {
		t.Fatal("snapshot should be ready after a successful collection")
	}
	if snap.Cluster.NodesTotal != 1 || snap.Cluster.NodesReady != 1 {
		t.Errorf("cluster nodes: got total=%d ready=%d", snap.Cluster.NodesTotal, snap.Cluster.NodesReady)
	}
	if snap.Cluster.PodsTotal != 1 {
		t.Errorf("want 1 pod, got %d", snap.Cluster.PodsTotal)
	}
	if len(snap.Microservices) != 1 || snap.Microservices[0].Name != "web" {
		t.Fatalf("want microservice %q, got %+v", "web", snap.Microservices)
	}
	if got := snap.Microservices[0]; got.ReadyReplicas != 1 || got.DesiredReplica != 1 {
		t.Errorf("microservice replicas: want ready=1 desired=1, got %+v", got)
	}

	hs := c.Health()
	if !hs.Ready {
		t.Fatal("collector should report ready after a successful collection")
	}
	if hs.LastError != "" {
		t.Errorf("want no error, got %q", hs.LastError)
	}
}

func TestCollectFailureLeavesHealthNotReady(t *testing.T) {
	src := oneNodeOnePodFixture()
	src.nodesErr = errors.New("api-server unreachable")
	c := New(src, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())

	c.collect(context.Background())

	hs := c.Health()
	if hs.Ready {
		t.Fatal("collector should not be ready when the poll failed")
	}
	if hs.LastError == "" {
		t.Error("want the last error to be recorded")
	}
	if c.Snapshot().Ready {
		t.Error("snapshot should stay un-ready when no collection has ever succeeded")
	}
}

func TestCollectRecoversAfterFailure(t *testing.T) {
	src := oneNodeOnePodFixture()
	c := New(src, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())

	src.podsErr = errors.New("timeout")
	c.collect(context.Background())
	if c.Health().Ready {
		t.Fatal("should not be ready after a failed poll")
	}

	src.podsErr = nil
	c.collect(context.Background())
	hs := c.Health()
	if !hs.Ready {
		t.Fatal("should recover to ready after a subsequent successful poll")
	}
	if hs.LastError != "" {
		t.Errorf("want error cleared after recovery, got %q", hs.LastError)
	}
	if !c.Snapshot().Ready {
		t.Error("snapshot should be ready once a collection has succeeded")
	}
}

func TestHealthGoesStaleWithoutRecentSuccess(t *testing.T) {
	src := oneNodeOnePodFixture()
	c := New(src, time.Hour, 5*time.Second, 10, func() bool { return true }, testLogger())
	c.collect(context.Background())
	if !c.Health().Ready {
		t.Fatal("expected ready right after a successful collection")
	}

	// Same package: reach in directly to simulate the passage of time
	// without a real sleep.
	c.mu.Lock()
	c.lastSuccess = time.Now().Add(-c.staleAfter - time.Second)
	c.mu.Unlock()

	if c.Health().Ready {
		t.Fatal("readiness should fail once the last success is older than staleAfter")
	}
}

func TestMetricsAvailableReflectsCallback(t *testing.T) {
	src := oneNodeOnePodFixture()
	available := true
	c := New(src, time.Hour, 5*time.Second, 10, func() bool { return available }, testLogger())

	c.collect(context.Background())
	if !c.Snapshot().MetricsAvailable {
		t.Fatal("want metrics available")
	}

	available = false
	c.collect(context.Background())
	if c.Snapshot().MetricsAvailable {
		t.Fatal("want metrics unavailable once the callback flips")
	}
}

func TestWorkloadOf(t *testing.T) {
	cases := []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{
			"replicaset owner strips the hash suffix",
			corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name:            "web-abc123xy",
				OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-abc123xy"}},
			}},
			"web",
		},
		{
			"statefulset owner keeps its name",
			corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: []metav1.OwnerReference{{Kind: "StatefulSet", Name: "db"}},
			}},
			"db",
		},
		{
			"no owner falls back to the app label",
			corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "legacy"}}},
			"legacy",
		},
		{
			"no owner or label falls back to the pod name",
			corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "standalone"}},
			"standalone",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := workloadOf(&c.pod); got != c.want {
				t.Errorf("want %q, got %q", c.want, got)
			}
		})
	}
}

func TestPctZeroCapacityIsZeroNotNaN(t *testing.T) {
	if got := pct(100, 0); got != 0 {
		t.Errorf("pct with zero capacity: want 0, got %v", got)
	}
}
