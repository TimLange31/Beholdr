// Package collect is the monitor service: it polls the cluster on an interval,
// aggregates everything the UI needs, and keeps a rolling in-memory history.
package collect

import (
	"context"
	"log/slog"
	"regexp"
	"sort"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/delangetimm/beholdr/internal/k8s"
)

var rsHash = regexp.MustCompile(`-[a-z0-9]{8,10}$`)

// Source is the subset of the k8s client the collector consumes (interface
// keeps the collector testable).
type Source interface {
	Nodes(context.Context) ([]corev1.Node, error)
	Pods(context.Context) ([]corev1.Pod, error)
	Deployments(context.Context) ([]appsv1.Deployment, error)
	HPAs(context.Context) ([]autoscalingv1.HorizontalPodAutoscaler, error)
	NodeMetrics(context.Context) map[string]k8s.Usage
	PodMetrics(context.Context) map[string]k8s.Usage
}

type Collector struct {
	src      Source
	interval time.Duration
	timeout  time.Duration
	log      *slog.Logger

	History *History

	mu   sync.RWMutex
	snap Snapshot

	metricsAvailable func() bool
}

func New(src Source, interval, timeout time.Duration, historySize int, metricsAvailable func() bool, log *slog.Logger) *Collector {
	return &Collector{
		src:              src,
		interval:         interval,
		timeout:          timeout,
		log:              log,
		History:          NewHistory(historySize),
		metricsAvailable: metricsAvailable,
	}
}

func (c *Collector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap
}

// Run polls until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	c.collect(ctx) // prime immediately
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.collect(ctx)
		}
	}
}

func (c *Collector) collect(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()

	nodes, err := c.src.Nodes(ctx)
	if err != nil {
		c.log.Error("list nodes", "err", err)
		return
	}
	pods, err := c.src.Pods(ctx)
	if err != nil {
		c.log.Error("list pods", "err", err)
		return
	}
	deployments, err := c.src.Deployments(ctx)
	if err != nil {
		c.log.Error("list deployments", "err", err)
		return
	}
	hpas, _ := c.src.HPAs(ctx)
	nodeUsage := c.src.NodeMetrics(ctx)
	podUsage := c.src.PodMetrics(ctx)

	ts := float64(time.Now().UnixNano()) / 1e9

	nodeMap := buildNodes(nodes, nodeUsage)
	podList, byNode, byMS := buildPods(pods, podUsage)
	msMap := buildMicroservices(deployments, hpas, byMS)

	for name, n := range nodeMap {
		np := byNode[name]
		n.Pods = np
		n.PodCount = len(np)
		n.Workloads = distinctWorkloads(np)
		nodeMap[name] = n
	}

	cluster := buildCluster(nodeMap, podList, msMap)

	// ---- history ----
	c.History.Push("cluster", Point{
		"t": ts, "cpu_pct": cluster.CPUPct, "mem_pct": cluster.MemPct,
		"cpu_used": float64(cluster.CPUUsed), "mem_used": float64(cluster.MemUsed),
		"pods_running": float64(cluster.PodsByPhase["Running"]),
	})
	keep := map[string]struct{}{"cluster": {}}
	for name, n := range nodeMap {
		key := "node::" + name
		keep[key] = struct{}{}
		c.History.Push(key, Point{
			"t": ts, "cpu_pct": n.CPUPct, "mem_pct": n.MemPct,
			"cpu_used": float64(n.CPUUsed), "mem_used": float64(n.MemUsed),
			"pod_count": float64(n.PodCount),
		})
	}
	for key, m := range msMap {
		hk := "ms::" + key
		keep[hk] = struct{}{}
		c.History.Push(hk, Point{
			"t": ts, "cpu_used": float64(m.CPUUsed), "mem_used": float64(m.MemUsed),
			"replicas_ready": float64(m.ReadyReplicas), "replicas_desired": float64(m.DesiredReplica),
		})
	}
	c.History.Prune(keep)

	snap := Snapshot{
		Ready:            true,
		UpdatedAt:        ts,
		MetricsAvailable: c.metricsAvailable(),
		Cluster:          cluster,
		Nodes:            sortedNodes(nodeMap),
		Microservices:    sortedMS(msMap),
		Pods:             podList,
	}
	c.mu.Lock()
	c.snap = snap
	c.mu.Unlock()
	c.log.Info("collected", "nodes", len(nodeMap), "pods", len(podList), "microservices", len(msMap))
}

// --- builders ---------------------------------------------------------------

func buildNodes(nodes []corev1.Node, usage map[string]k8s.Usage) map[string]Node {
	out := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		cpuCap := n.Status.Capacity.Cpu().MilliValue()
		memCap := n.Status.Capacity.Memory().Value()
		u := usage[n.Name]
		ready := false
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		roles := []string{}
		for k := range n.Labels {
			const p = "node-role.kubernetes.io/"
			if len(k) > len(p) && k[:len(p)] == p {
				roles = append(roles, k[len(p):])
			}
		}
		if len(roles) == 0 {
			roles = []string{"worker"}
		}
		out[n.Name] = Node{
			Name:           n.Name,
			Ready:          ready,
			Roles:          roles,
			KubeletVersion: n.Status.NodeInfo.KubeletVersion,
			CPUCapacity:    cpuCap,
			MemCapacity:    memCap,
			CPUAllocatable: n.Status.Allocatable.Cpu().MilliValue(),
			MemAllocatable: n.Status.Allocatable.Memory().Value(),
			CPUUsed:        u.CPUMilli,
			MemUsed:        u.MemBytes,
			CPUPct:         pct(u.CPUMilli, cpuCap),
			MemPct:         pct(u.MemBytes, memCap),
		}
	}
	return out
}

func buildPods(pods []corev1.Pod, usage map[string]k8s.Usage) ([]Pod, map[string][]Pod, map[string][]Pod) {
	list := make([]Pod, 0, len(pods))
	byNode := map[string][]Pod{}
	byMS := map[string][]Pod{}
	for i := range pods {
		p := &pods[i]
		workload := workloadOf(p)
		reqCPU, reqMem := podRequests(p)
		u := usage[p.Namespace+"/"+p.Name]
		var restarts int32
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}
		e := Pod{
			Namespace:  p.Namespace,
			Name:       p.Name,
			Node:       p.Spec.NodeName,
			Workload:   workload,
			Phase:      string(p.Status.Phase),
			Restarts:   restarts,
			CPUUsed:    u.CPUMilli,
			MemUsed:    u.MemBytes,
			CPURequest: reqCPU,
			MemRequest: reqMem,
		}
		list = append(list, e)
		if e.Node != "" {
			byNode[e.Node] = append(byNode[e.Node], e)
		}
		byMS[p.Namespace+"/"+workload] = append(byMS[p.Namespace+"/"+workload], e)
	}
	return list, byNode, byMS
}

func buildMicroservices(deps []appsv1.Deployment, hpas []autoscalingv1.HorizontalPodAutoscaler, byMS map[string][]Pod) map[string]Microservice {
	hpaByTarget := map[string]autoscalingv1.HorizontalPodAutoscaler{}
	for _, h := range hpas {
		hpaByTarget[h.Namespace+"/"+h.Spec.ScaleTargetRef.Name] = h
	}
	out := map[string]Microservice{}
	seen := map[string]bool{}
	for i := range deps {
		d := &deps[i]
		key := d.Namespace + "/" + d.Name
		seen[key] = true
		pods := byMS[key]
		var hpa *autoscalingv1.HorizontalPodAutoscaler
		if h, ok := hpaByTarget[key]; ok {
			hpa = &h
		}
		desired := int32(len(pods))
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		out[key] = msEntry(d.Namespace, d.Name, "Deployment", desired, d.Status.ReadyReplicas, pods, hpa)
	}
	// workloads with pods but no matching Deployment
	for key, pods := range byMS {
		if seen[key] {
			continue
		}
		ns, name := splitKey(key)
		var hpa *autoscalingv1.HorizontalPodAutoscaler
		if h, ok := hpaByTarget[key]; ok {
			hpa = &h
		}
		out[key] = msEntry(ns, name, "Other", int32(len(pods)), 0, pods, hpa)
	}
	return out
}

func msEntry(ns, name, kind string, desired, ready int32, pods []Pod, hpa *autoscalingv1.HorizontalPodAutoscaler) Microservice {
	var cpuUsed, memUsed, cpuReq, memReq int64
	var restarts int32
	running := 0
	nodeSet := map[string]struct{}{}
	for _, p := range pods {
		cpuUsed += p.CPUUsed
		memUsed += p.MemUsed
		cpuReq += p.CPURequest
		memReq += p.MemRequest
		restarts += p.Restarts
		if p.Phase == "Running" {
			running++
		}
		if p.Node != "" {
			nodeSet[p.Node] = struct{}{}
		}
	}
	if ready == 0 {
		ready = int32(running)
	}
	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)

	var util *float64
	if cpuReq > 0 {
		v := round1(100 * float64(cpuUsed) / float64(cpuReq))
		util = &v
	}
	m := Microservice{
		Key: ns + "/" + name, Namespace: ns, Name: name, Kind: kind,
		DesiredReplica: desired, ReadyReplicas: ready, RunningPods: running,
		Restarts: restarts, Nodes: nodes,
		CPUUsed: cpuUsed, MemUsed: memUsed, CPURequest: cpuReq, MemRequest: memReq,
		CPUUtilPct: util,
	}
	if hpa != nil {
		h := HPA{
			Max:           hpa.Spec.MaxReplicas,
			Current:       hpa.Status.CurrentReplicas,
			Desired:       hpa.Status.DesiredReplicas,
			TargetCPUPct:  hpa.Spec.TargetCPUUtilizationPercentage,
			CurrentCPUPct: hpa.Status.CurrentCPUUtilizationPercentage,
		}
		if hpa.Spec.MinReplicas != nil {
			h.Min = *hpa.Spec.MinReplicas
		}
		m.HPA = &h
	}
	return m
}

func buildCluster(nodeMap map[string]Node, pods []Pod, ms map[string]Microservice) Cluster {
	var cpuCap, memCap, cpuUsed, memUsed int64
	ready := 0
	for _, n := range nodeMap {
		cpuCap += n.CPUCapacity
		memCap += n.MemCapacity
		cpuUsed += n.CPUUsed
		memUsed += n.MemUsed
		if n.Ready {
			ready++
		}
	}
	byPhase := map[string]int{}
	for _, p := range pods {
		ph := p.Phase
		if ph == "" {
			ph = "Unknown"
		}
		byPhase[ph]++
	}
	return Cluster{
		NodesTotal: len(nodeMap), NodesReady: ready,
		MicroservicesTotal: len(ms), PodsTotal: len(pods), PodsByPhase: byPhase,
		CPUCapacity: cpuCap, MemCapacity: memCap, CPUUsed: cpuUsed, MemUsed: memUsed,
		CPUPct: pct(cpuUsed, cpuCap), MemPct: pct(memUsed, memCap),
	}
}

// --- helpers ----------------------------------------------------------------

func workloadOf(p *corev1.Pod) string {
	for _, ref := range p.OwnerReferences {
		switch ref.Kind {
		case "ReplicaSet":
			return rsHash.ReplaceAllString(ref.Name, "")
		case "StatefulSet", "DaemonSet", "Job":
			return ref.Name
		}
	}
	if v, ok := p.Labels["app"]; ok {
		return v
	}
	if v, ok := p.Labels["app.kubernetes.io/name"]; ok {
		return v
	}
	return p.Name
}

func podRequests(p *corev1.Pod) (cpu, mem int64) {
	for _, c := range p.Spec.Containers {
		if r := c.Resources.Requests; r != nil {
			cpu += r.Cpu().MilliValue()
			mem += r.Memory().Value()
		}
	}
	return
}

func distinctWorkloads(pods []Pod) []string {
	set := map[string]struct{}{}
	for _, p := range pods {
		set[p.Workload] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for w := range set {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

func sortedNodes(m map[string]Node) []Node {
	out := make([]Node, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedMS(m map[string]Microservice) []Microservice {
	out := make([]Microservice, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPUUsed > out[j].CPUUsed })
	return out
}

func splitKey(k string) (ns, name string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '/' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

func pct(used, cap int64) float64 {
	if cap <= 0 {
		return 0
	}
	return round1(100 * float64(used) / float64(cap))
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}
