variable "kubeconfig" {
  description = "Path to kubeconfig used by Terraform to talk to the cluster."
  type        = string
  default     = "~/.kube/config"
}

variable "kube_context" {
  description = "kubeconfig context to target (empty = current)."
  type        = string
  default     = ""
}

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
  description = "Extra annotations for the Ingress (TLS, auth, rewrite, etc.)."
  type        = map(string)
  default     = {}
}
