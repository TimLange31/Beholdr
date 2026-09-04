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

export interface Health {
  ok: boolean;
  ready: boolean;
  last_success: number;
  last_error: string;
  last_error_at: number;
  metrics_available: boolean;
}

export interface IntegrationProvider {
  name: string;
  signal: string;
  configured: boolean;
  reachable: boolean;
  /** The backend answered but reported itself unhealthy (e.g. an ES red cluster). */
  degraded?: boolean;
  /** Certificate verification is switched off for this backend. */
  tls_skip_verify?: boolean;
  checked_at: number;
  latency_ms: number;
  /** Sanitized health summary from the backend; never raw upstream text. */
  detail?: string;
  error?: string;
}

export interface IntegrationStatus {
  updated_at: number;
  providers: IntegrationProvider[];
}

export type ServiceSeverity = "unknown" | "healthy" | "warning" | "critical";

// "no_data" means the metric does not exist for this workload (no memory limit
// set, no HTTP traffic); "error" means the query itself failed. Only the second
// makes the service's aggregate state unknown.
export type ServiceSignalState = "ok" | "no_data" | "error";

export interface ServiceMetricLine {
  key: string;
  label: string;
  color: string;
}

export interface ServiceMetricSignal {
  key: string;
  label: string;
  unit: string;
  description: string;
  current?: number;
  previous?: number;
  difference?: number;
  /** Absent when there is no meaningful warning band (a single-replica service). */
  warning?: number;
  critical: number;
  severity: ServiceSeverity;
  state: ServiceSignalState;
  lines: ServiceMetricLine[];
  points: Point[];
  error?: string;
}

export interface ServiceMetricsReport {
  namespace: string;
  service: string;
  window: string;
  start: number;
  end: number;
  step: number;
  severity: ServiceSeverity;
  signals: ServiceMetricSignal[];
  /** False on windows longer than a week, where a week-before overlay would
   *  overlap the current series instead of comparing against it. */
  compared: boolean;
}
