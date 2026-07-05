package collect

// JSON shapes served by the API. Field names are the stable contract the UI
// depends on.

type Cluster struct {
	NodesTotal          int            `json:"nodes_total"`
	NodesReady          int            `json:"nodes_ready"`
	MicroservicesTotal  int            `json:"microservices_total"`
	PodsTotal           int            `json:"pods_total"`
	PodsByPhase         map[string]int `json:"pods_by_phase"`
	CPUCapacity         int64          `json:"cpu_capacity"`
	MemCapacity         int64          `json:"mem_capacity"`
	CPUUsed             int64          `json:"cpu_used"`
	MemUsed             int64          `json:"mem_used"`
	CPUPct              float64        `json:"cpu_pct"`
	MemPct              float64        `json:"mem_pct"`
}

type Node struct {
	Name           string   `json:"name"`
	Ready          bool     `json:"ready"`
	Roles          []string `json:"roles"`
	KubeletVersion string   `json:"kubelet_version"`
	CPUCapacity    int64    `json:"cpu_capacity"`
	MemCapacity    int64    `json:"mem_capacity"`
	CPUAllocatable int64    `json:"cpu_allocatable"`
	MemAllocatable int64    `json:"mem_allocatable"`
	CPUUsed        int64    `json:"cpu_used"`
	MemUsed        int64    `json:"mem_used"`
	CPUPct         float64  `json:"cpu_pct"`
	MemPct         float64  `json:"mem_pct"`
	PodCount       int      `json:"pod_count"`
	Workloads      []string `json:"workloads"`
	Pods           []Pod    `json:"pods,omitempty"`
}

type Pod struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Node       string `json:"node"`
	Workload   string `json:"workload"`
	Phase      string `json:"phase"`
	Restarts   int32  `json:"restarts"`
	CPUUsed    int64  `json:"cpu_used"`
	MemUsed    int64  `json:"mem_used"`
	CPURequest int64  `json:"cpu_request"`
	MemRequest int64  `json:"mem_request"`
}

type HPA struct {
	Min           int32 `json:"min"`
	Max           int32 `json:"max"`
	Current       int32 `json:"current"`
	Desired       int32 `json:"desired"`
	TargetCPUPct  *int32 `json:"target_cpu_pct"`
	CurrentCPUPct *int32 `json:"current_cpu_pct"`
}

type Microservice struct {
	Key            string   `json:"key"`
	Namespace      string   `json:"namespace"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	DesiredReplica int32    `json:"desired_replicas"`
	ReadyReplicas  int32    `json:"ready_replicas"`
	RunningPods    int      `json:"running_pods"`
	Restarts       int32    `json:"restarts"`
	Nodes          []string `json:"nodes"`
	CPUUsed        int64    `json:"cpu_used"`
	MemUsed        int64    `json:"mem_used"`
	CPURequest     int64    `json:"cpu_request"`
	MemRequest     int64    `json:"mem_request"`
	CPUUtilPct     *float64 `json:"cpu_util_pct"`
	HPA            *HPA     `json:"hpa"`
}

// Snapshot is the full consolidated view produced each poll cycle.
type Snapshot struct {
	Ready            bool           `json:"ready"`
	UpdatedAt        float64        `json:"updated_at"`
	MetricsAvailable bool           `json:"metrics_available"`
	Cluster          Cluster        `json:"cluster"`
	Nodes            []Node         `json:"nodes"`
	Microservices    []Microservice `json:"microservices"`
	Pods             []Pod          `json:"pods"`
}
