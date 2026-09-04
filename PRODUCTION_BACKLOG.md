# Beholdr production backlog

## Product decision

Beholdr is currently a lightweight Kubernetes resource observer. It is **not**
yet a replacement for New Relic, Grafana, or Prometheus: it only reads the
Kubernetes API and `metrics.k8s.io`, retains about one hour of in-memory CPU,
memory, pod, and replica samples, and has no alerting, logs, traces, durable
storage, authentication, or arbitrary metric query capability.

The recommended release path is therefore two tracks:

1. Make the existing observer safe and dependable for production use.
2. Design and build an observability platform deliberately, instead of trying
   to extend the polling loop into a Prometheus/New Relic substitute.

Priorities: **P0** blocks a production release; **P1** belongs in the first
production-ready release; **P2** is planned follow-up.

## P0 — release blockers

- [ ] **Correct health semantics.** Split liveness from readiness. Liveness
  should only establish that the process can serve requests; readiness must
  fail until there has been a successful collection and when its data is stale.
  Return the last-success time and latest collection error. Point the two
  Kubernetes probes at the appropriate endpoints.  
  _Why:_ `/api/health` currently returns HTTP 200 even when `ready` is false,
  so the deployment can receive traffic before it has usable data.

- [ ] **Secure the UI and API by default.** Set CORS off by default, support an
  explicit allowlist of origins, and document a required authentication model
  (for example an OIDC-aware ingress/proxy). Require TLS in the production
  ingress example and reject/flag insecure production configuration.  
  _Why:_ the default is permissive CORS and the supplied ingress has neither
  TLS nor authentication, exposing cluster topology, pod names, and resource
  usage.

- [ ] **Make collector health and metric availability correct under failure.**
  Synchronize client state or return availability/errors as part of each
  collection result; recover to “available” after a later successful metrics
  read; show partial-data and stale-data states in the UI.  
  _Why:_ `MetricsAvailable` is read and written from concurrent goroutines,
  becomes permanently false after one error, and current zero values are
  indistinguishable from missing metrics.

- [ ] **Create a repeatable, clean build.** Commit `go.sum` and the web lock
  file; use `npm ci` in the container; pin base images and GitHub Actions by
  digest/immutable revision; generate an SBOM and scan image/dependencies in
  CI.  
  _Why:_ the repository has no Go checksum file and ignores the frontend lock
  file, while the Docker build resolves dependencies dynamically.

- [ ] **Establish automated quality gates.** Add unit tests for configuration,
  collector aggregation/failure paths, API status/headers, and history bounds;
  add frontend type/check/build tests; run all checks, container build, IaC
  validation, vulnerability scan, and license checks on pull requests. Publish
  coverage and fail on regressions.  
  _Why:_ there are no tests and CI only publishes an image on tags/manual runs.

- [ ] **Fix documented-versus-actual workload coverage.** Either implement
  StatefulSet and DaemonSet discovery with their real desired/ready status, or
  remove the claim that they are first-class microservices. Handle Jobs and
  CronJobs explicitly rather than grouping them as “Other”.

- [ ] **Run a security and scale validation against a representative cluster.**
  Validate least-privilege RBAC, namespace isolation, API response size,
  collection duration, API-server request rate, memory growth, and UI behavior
  at the target pod/node/workload counts. Define and test a supported scale
  envelope before launch.

## P1 — production-ready observer

- [ ] **Persist history and define retention.** Add a pluggable durable store,
  migrations, retention/downsampling, backups/restores, and a graceful
  degradation path. Keep an in-memory option for local/demo use.

- [ ] **Provide high availability.** Make collectors horizontally safe via
  leader election/sharding and shared storage, add a PodDisruptionBudget, and
  document recovery and upgrade behavior. A single in-memory replica must not
  be the source of truth for operational data.

- [ ] **Use Kubernetes watches/informers.** Replace repeated full object lists
  with shared informers and cache state; retain periodic reconciliation as a
  correctness backstop. Bound concurrency and expose collection latency/error
  metrics.

- [ ] **Improve resource accuracy.** Calculate scheduling pressure from
  allocatable (not raw capacity), expose requests *and* limits, account for
  init containers/pod overhead, distinguish usage samples from instantaneous
  utilization, and surface missing or stale metrics explicitly.

- [ ] **Cover core Kubernetes operational signals.** Add pod/container state
  reasons, OOMKills, CrashLoopBackOff, restart rate, image/version, owner
  hierarchy, rollout progress, unschedulable pods, node conditions/pressure,
  PVC status, and Kubernetes Events with correlation to each workload.

- [ ] **Add filtering and safe API contracts.** Implement pagination,
  namespace/label/status filters, sorting, server-side time-range queries,
  stable versioned API schemas, validation and encoded path handling, request
  limits/timeouts, compression, and a generated OpenAPI contract.

- [ ] **Improve the operator experience.** Add global search, a unified
  workload/pod detail route, time-range selection and zoom, table sorting,
  empty/error/stale states, saved URL filters, responsive/mobile layout, and
  accessibility and keyboard checks.

- [ ] **Operate Beholdr itself.** Expose Prometheus-format self-metrics,
  structured request logs, traces, profiling, audit logs, dashboards, and an
  operational runbook (install, upgrade, rollback, recovery, and incident
  triage).

- [ ] **Harden deployment defaults.** Add security context (read-only root
  filesystem, dropped capabilities, seccomp), NetworkPolicy guidance, resource
  sizing, image pull policy, PDB, pod anti-affinity/topology spread where HA is
  enabled, and a Helm chart with values/schema validation. Keep raw manifest
  and Terraform outputs aligned.

- [ ] **Document supported environments and failure modes.** Include
  metrics-server requirements, Kubernetes version matrix, RBAC modes,
  namespace-scoped deployment option, capacity limits, data-retention
  guarantees, and troubleshooting. Remove or add the missing Terraform README
  referenced by the root README.

## P1 — platform architecture decision (required before claiming replacement)

- [ ] **Write an observability architecture RFC.** Decide whether Beholdr will
  integrate with Prometheus-compatible/OpenTelemetry systems or own ingestion,
  storage, query, and alert evaluation. Define tenancy, data model, retention,
  cardinality limits, cost model, availability SLOs, and a migration path.

- [ ] **Adopt OpenTelemetry as the integration contract.** Support OTLP for
  traces, metrics, and logs; propagate service/resource attributes; provide
  Kubernetes enrichment; and publish language/platform onboarding examples.

- [ ] **Build a metrics platform.** Ingest application, Kubernetes, host,
  network, and custom metrics; support labels, dimensional aggregation,
  recording rules, remote write/read or an equivalent durable protocol,
  cardinality controls, and a query language/API. The Kubernetes metrics API
  alone cannot supply this.

- [ ] **Build a dashboard and exploration layer.** Add composable dashboards,
  panels, variables, annotations, ad-hoc queries, sharing/export, provisioning
  as code, permissions, and a metric/log/trace explorer. This is the minimum
  Grafana-like workflow, not just fixed pages.

- [ ] **Build alerting and incident response.** Add alert rules, evaluation and
  deduplication, silences, routing/escalation, notification integrations,
  alert history, runbook links, and SLO/error-budget alerts. Test delivery and
  failure behavior end to end.

- [ ] **Build logs and trace correlation.** Add indexed log ingestion/search
  with retention controls, distributed trace storage/search, service maps,
  error analytics, and links between deploys, events, metrics, logs, and
  traces. Add browser RUM and synthetics only after the core backend signals.

- [ ] **Design identity and tenancy.** Implement SSO (OIDC/SAML as required),
  RBAC, teams/projects, per-tenant quotas and retention, audit trails, data
  isolation, and secret-management policies before accepting multiple users or
  clusters.

- [ ] **Support multi-cluster and lifecycle management.** Provide a secure
  agent/collector deployment model, central control plane or federation,
  upgrades, version compatibility, configuration-as-code, health reporting,
  and cost/usage accounting.

## P2 — high-value differentiators

- [ ] **Kubernetes cost and capacity intelligence:** rightsizing from usage
  history, request/limit recommendations, bin-packing visibility, idle
  workloads, and cost allocation by team/namespace/label.
- [ ] **Change intelligence:** correlate deploys, configuration changes,
  autoscaling, events, and incidents; add rollback-risk and anomaly views.
- [ ] **Service health views:** golden signals, dependency/service maps, SLO
  scorecards, error-budget forecasting, and release-health comparison.
- [ ] **Integrations:** Alertmanager-compatible webhooks, Slack/PagerDuty/email,
  GitHub/GitOps annotations, cloud-provider metrics, databases, and managed
  Kubernetes distributions.

## Suggested release gates

Do not market Beholdr as a replacement until the platform-architecture items
are implemented and independently load/security tested. For the near-term
observer release, all P0 items must be complete, P1 observer work must have an
explicitly accepted scope, and the release must pass a representative-cluster
test with a documented supported scale and recovery exercise.
