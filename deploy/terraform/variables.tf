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

variable "tls_secret_name" {
  description = "TLS secret name for the Ingress (e.g. one cert-manager creates via a cluster-issuer annotation in ingress_annotations, or one you manage yourself). Required unless insecure_http is explicitly set — Beholdr exposes cluster topology, pod names, and resource usage."
  type        = string
  default     = ""
}

variable "insecure_http" {
  description = "Explicitly allow deploying the Ingress without TLS. Do not use in production."
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
