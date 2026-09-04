variable "namespace" {
  description = "Namespace Beholdr is deployed into."
  type        = string
  default     = "beholdr"
}

variable "image" {
  description = "Fully-qualified Beholdr image, e.g. registry.example.com/beholdr:0.1.0"
  type        = string
}

variable "replicas" {
  description = "Number of Beholdr replicas (1 is plenty — state is in-memory per pod)."
  type        = number
  default     = 1
}

variable "poll_interval_seconds" {
  description = "How often the collector polls the cluster."
  type        = number
  default     = 15
}

variable "history_size" {
  description = "Samples retained per series in memory (240 * poll_interval = window)."
  type        = number
  default     = 240
}

variable "watch_namespaces" {
  description = "Restrict monitoring to these namespaces. Empty = whole cluster."
  type        = list(string)
  default     = []
}

variable "ingress_enabled" {
  description = "Create an Ingress for the web UI."
  type        = bool
  default     = true
}

variable "ingress_class" {
  description = "Ingress class name (e.g. nginx, traefik)."
  type        = string
  default     = "nginx"
}

variable "ingress_host" {
  description = "Hostname for the Beholdr UI, e.g. beholdr.example.com"
  type        = string
  default     = "beholdr.local"
}

variable "ingress_annotations" {
  description = "Extra annotations for the Ingress (rewrite rules, etc. — see auth_annotations for authentication and tls_secret_name for TLS)."
  type        = map(string)
  default     = {}
}

variable "tls_mode" {
  description = <<-EOT
    Where TLS for the Beholdr hostname is terminated.

      "cluster" (default) — the Ingress serves the certificate in tls_secret_name
        (cert-manager, or one you manage). This also covers Cloudflare Full and
        Full (strict): put a Cloudflare Origin CA certificate in that secret.

      "edge" — something in front of the cluster terminates TLS and reaches this
        Ingress over plain HTTP: Cloudflare Flexible SSL, a Cloudflare Tunnel, or
        an external load balancer. No certificate or key is created, requested or
        mounted in the cluster, and ssl-redirect is switched off so the edge's
        HTTPS rewrite cannot bounce against an origin redirect. Name the
        terminator in edge_tls_terminator, and restrict who may reach the origin
        with edge_source_ranges.
  EOT
  type        = string
  default     = "cluster"

  validation {
    condition     = contains(["cluster", "edge"], var.tls_mode)
    error_message = "tls_mode must be \"cluster\" or \"edge\"."
  }
}

variable "edge_tls_terminator" {
  description = "What terminates TLS when tls_mode = \"edge\" (e.g. \"cloudflare\", \"cloudflare-tunnel\", \"azure-front-door\"). Required in that mode: it is the recorded reason this Ingress carries no certificate."
  type        = string
  default     = ""
}

variable "edge_source_ranges" {
  description = "CIDRs allowed to reach the Ingress when tls_mode = \"edge\" (ingress-nginx whitelist-source-range). Strongly recommended: in edge mode the origin speaks plain HTTP, so without this anything that can route to the Ingress bypasses the edge and its authentication. For Cloudflare, use the published IP ranges from https://www.cloudflare.com/ips/ — or avoid origin exposure entirely with a Cloudflare Tunnel."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.edge_source_ranges : can(cidrhost(c, 0))])
    error_message = "edge_source_ranges must contain valid IPv4/IPv6 CIDRs, e.g. \"173.245.48.0/20\"."
  }
}

variable "tls_secret_name" {
  description = "TLS secret name for the Ingress when tls_mode = \"cluster\" (e.g. one cert-manager creates via a cluster-issuer annotation in ingress_annotations, a Cloudflare Origin CA certificate, or one you manage yourself). Required in that mode unless insecure_http is explicitly set — Beholdr exposes cluster topology, pod names, and resource usage. Must be empty when tls_mode = \"edge\"."
  type        = string
  default     = ""
}

variable "insecure_http" {
  description = "Explicitly allow deploying the Ingress without TLS anywhere — no certificate here and none in front. Do not use in production; tls_mode = \"edge\" is the supported way to run without an origin certificate."
  type        = bool
  default     = false
}

variable "auth_annotations" {
  description = "Ingress annotations that enforce authentication in front of Beholdr, e.g. an OIDC-aware auth proxy such as oauth2-proxy: { \"nginx.ingress.kubernetes.io/auth-url\" = \"https://oauth2-proxy.example.com/oauth2/auth\", \"nginx.ingress.kubernetes.io/auth-signin\" = \"https://oauth2-proxy.example.com/oauth2/start?rd=$scheme://$host$request_uri\" }. Required unless insecure_no_auth is explicitly set — Beholdr has no built-in authentication."
  type        = map(string)
  default     = {}
}

variable "insecure_no_auth" {
  description = "Explicitly allow deploying the Ingress without an authentication annotation. Do not use in production."
  type        = bool
  default     = false
}

variable "cors_origins" {
  description = "Explicit CORS allowlist for the API (BEHOLDR_CORS_ORIGINS). Empty disables CORS entirely (the default) — only set this if a separately-hosted frontend needs to call the API cross-origin."
  type        = list(string)
  default     = []
}

variable "prometheus_url" {
  description = "Prometheus base URL, e.g. http://prometheus.monitoring.svc.cluster.local:9090. The health check calls /-/ready beneath it, so this is the server root, not the /api/v1 query path. Empty disables the integration."
  type        = string
  default     = ""

  validation {
    condition     = var.prometheus_url == "" || can(regex("^https?://", var.prometheus_url))
    error_message = "prometheus_url must start with http:// or https://."
  }
}

variable "prometheus_bearer_token_secret_name" {
  description = "Existing Kubernetes Secret containing the optional Prometheus bearer token. Empty omits the token."
  type        = string
  default     = ""
}

variable "prometheus_bearer_token_secret_key" {
  description = "Key in prometheus_bearer_token_secret_name."
  type        = string
  default     = "token"
}

variable "elasticsearch_url" {
  description = "Elasticsearch base URL. The health check calls /_cluster/health?local=true beneath it. Empty disables the integration."
  type        = string
  default     = ""

  validation {
    condition     = var.elasticsearch_url == "" || can(regex("^https?://", var.elasticsearch_url))
    error_message = "elasticsearch_url must start with http:// or https://."
  }
}

variable "elasticsearch_api_key_secret_name" {
  description = "Existing Kubernetes Secret containing the optional Elasticsearch API key. Empty omits the key."
  type        = string
  default     = ""
}

variable "elasticsearch_api_key_secret_key" {
  description = "Key in elasticsearch_api_key_secret_name."
  type        = string
  default     = "api-key"
}

variable "otel_collector_health_url" {
  description = "OpenTelemetry Collector health_check extension URL, commonly port 13133. Unlike the other two this is the full URL that is requested, not a base. Empty disables the integration."
  type        = string
  default     = ""

  validation {
    condition     = var.otel_collector_health_url == "" || can(regex("^https?://", var.otel_collector_health_url))
    error_message = "otel_collector_health_url must start with http:// or https://."
  }
}

variable "integration_check_interval_seconds" {
  description = "How often Beholdr checks external telemetry systems."
  type        = number
  default     = 30

  validation {
    condition     = var.integration_check_interval_seconds > 0
    error_message = "integration_check_interval_seconds must be greater than 0; Beholdr would otherwise silently fall back to its own 30s default."
  }
}

variable "integration_request_timeout_seconds" {
  description = "Per-request timeout for external telemetry system health checks."
  type        = number
  default     = 5

  # The interval/timeout relationship is checked as a precondition on the
  # Deployment instead: referring to another variable inside a validation block
  # needs Terraform 1.9, and this module supports 1.4.
  validation {
    condition     = var.integration_request_timeout_seconds > 0
    error_message = "integration_request_timeout_seconds must be greater than 0; Beholdr would otherwise silently fall back to its own 5s default."
  }
}

# --- outbound TLS trust ------------------------------------------------------

variable "integration_ca_secret_name" {
  description = "Existing Kubernetes Secret holding CA bundles for the telemetry backends, mounted read-only at /etc/beholdr/ca. Needed when a backend presents a certificate the system trust store does not know: an ECK-issued Elasticsearch cert, a Cloudflare Origin CA cert, a corporate PKI. Empty mounts nothing."
  type        = string
  default     = ""
}

variable "prometheus_ca_secret_key" {
  description = "Key in integration_ca_secret_name holding the PEM bundle that signs Prometheus's certificate. Empty uses the system trust store."
  type        = string
  default     = ""
}

variable "elasticsearch_ca_secret_key" {
  description = "Key in integration_ca_secret_name holding the PEM bundle that signs Elasticsearch's certificate (for ECK this is the ca.crt in <cluster>-es-http-certs-public). Empty uses the system trust store."
  type        = string
  default     = ""
}

variable "otel_collector_ca_secret_key" {
  description = "Key in integration_ca_secret_name holding the PEM bundle that signs the Collector's certificate. Empty uses the system trust store."
  type        = string
  default     = ""
}

variable "prometheus_tls_insecure" {
  description = "Skip certificate verification for Prometheus. Last resort only — prefer prometheus_ca_secret_key. Beholdr logs this at WARN on startup and shows the provider as unverified in the UI so it cannot quietly become permanent."
  type        = bool
  default     = false
}

variable "elasticsearch_tls_insecure" {
  description = "Skip certificate verification for Elasticsearch. Last resort only — prefer elasticsearch_ca_secret_key. Surfaced at WARN and in the UI."
  type        = bool
  default     = false
}

variable "otel_collector_tls_insecure" {
  description = "Skip certificate verification for the OpenTelemetry Collector. Last resort only — prefer otel_collector_ca_secret_key. Surfaced at WARN and in the UI."
  type        = bool
  default     = false
}
