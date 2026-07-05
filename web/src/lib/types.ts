export interface Cluster {
  nodes_total: number; nodes_ready: number; microservices_total: number;
  pods_total: number; pods_by_phase: Record<string, number>;
  cpu_capacity: number; mem_capacity: number; cpu_used: number; mem_used: number;
  cpu_pct: number; mem_pct: number;
}
export interface NodeInfo {
  name: string; ready: boolean; roles: string[]; kubelet_version: string;
  cpu_capacity: number; mem_capacity: number; cpu_used: number; mem_used: number;
  cpu_pct: number; mem_pct: number; pod_count: number; workloads: string[];
  pods?: PodInfo[];
}
export interface PodInfo {
  namespace: string; name: string; node: string; workload: string;
  phase: string; restarts: number; cpu_used: number; mem_used: number;
  cpu_request: number; mem_request: number;
}
export interface Hpa {
  min: number; max: number; current: number; desired: number;
  target_cpu_pct: number | null; current_cpu_pct: number | null;
}
export interface Microservice {
  key: string; namespace: string; name: string; kind: string;
  desired_replicas: number; ready_replicas: number; running_pods: number;
  restarts: number; nodes: string[]; cpu_used: number; mem_used: number;
  cpu_request: number; mem_request: number; cpu_util_pct: number | null; hpa: Hpa | null;
}
export type Point = Record<string, number>;
