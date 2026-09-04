terraform {
  required_version = ">= 1.4"
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.23"
    }
  }
}

# NOTE: This module does not configure the kubernetes provider itself.
# The calling (root) module must supply a configured `kubernetes` provider,
# e.g. pointed at the target AKS cluster. See deploy/terraform/README for a
# standalone-usage wrapper.

locals {
  name   = "beholdr"
  labels = { "app.kubernetes.io/name" = "beholdr", "app.kubernetes.io/part-of" = "beholdr" }

  # Outbound TLS trust. One Secret holds every CA bundle Beholdr needs to talk
  # to the telemetry backends; each provider points at a key inside it.
  ca_mount_path = "/etc/beholdr/ca"
  ca_mounted    = var.integration_ca_secret_name != ""
  ca_file = {
    prometheus    = local.ca_mounted && var.prometheus_ca_secret_key != "" ? "${local.ca_mount_path}/${var.prometheus_ca_secret_key}" : ""
    elasticsearch = local.ca_mounted && var.elasticsearch_ca_secret_key != "" ? "${local.ca_mount_path}/${var.elasticsearch_ca_secret_key}" : ""
    otel          = local.ca_mounted && var.otel_collector_ca_secret_key != "" ? "${local.ca_mount_path}/${var.otel_collector_ca_secret_key}" : ""
  }

  nginx = var.ingress_class == "nginx"

  # Service-health configuration. Every entry is optional: an empty value is
  # omitted so Beholdr applies its own default rather than being handed an
  # empty string it would have to interpret.
  service_health_env = {
    BEHOLDR_SERVICE_HTTP_REQUESTS_METRIC   = var.service_http_requests_metric
    BEHOLDR_SERVICE_HTTP_ERRORS_METRIC     = var.service_http_errors_metric
    BEHOLDR_SERVICE_HTTP_STATUS_LABEL      = var.service_http_status_label
    BEHOLDR_SERVICE_APP_NAMESPACE_LABEL    = var.service_app_namespace_label
    BEHOLDR_SERVICE_APP_SERVICE_LABEL      = var.service_app_service_label
    BEHOLDR_SERVICE_APP_POD_LABEL          = var.service_app_pod_label
    BEHOLDR_SERVICE_KUBE_NAMESPACE_LABEL   = var.service_kube_namespace_label
    BEHOLDR_SERVICE_KUBE_POD_LABEL         = var.service_kube_pod_label
    BEHOLDR_SERVICE_CPU_BASIS              = var.service_cpu_basis
    BEHOLDR_SERVICE_ERROR_RATE_WARNING     = try(tostring(var.service_thresholds.error_rate_warning), "")
    BEHOLDR_SERVICE_ERROR_RATE_CRITICAL    = try(tostring(var.service_thresholds.error_rate_critical), "")
    BEHOLDR_SERVICE_ERROR_INCREASE_WARNING = try(tostring(var.service_thresholds.error_increase_warning), "")
    BEHOLDR_SERVICE_ERROR_INCREASE_CRITICAL = try(tostring(var.service_thresholds.error_increase_critical), "")
    BEHOLDR_SERVICE_CPU_WARNING            = try(tostring(var.service_thresholds.cpu_warning), "")
    BEHOLDR_SERVICE_CPU_CRITICAL           = try(tostring(var.service_thresholds.cpu_critical), "")
    BEHOLDR_SERVICE_MEMORY_WARNING         = try(tostring(var.service_thresholds.memory_warning), "")
    BEHOLDR_SERVICE_MEMORY_CRITICAL        = try(tostring(var.service_thresholds.memory_critical), "")
    BEHOLDR_SERVICE_FAILING_PODS_WARNING   = try(tostring(var.service_thresholds.failing_pods_warning), "")
    BEHOLDR_SERVICE_FAILING_PODS_CRITICAL  = try(tostring(var.service_thresholds.failing_pods_critical), "")
  }

  # In edge mode the public hostname is already HTTPS-only at the terminator
  # (Cloudflare "Always Use HTTPS"/automatic rewrites, or an external LB), and
  # the hop from there to this Ingress is plain HTTP. Redirecting to https here
  # would bounce that hop straight back to the terminator, which re-requests
  # over HTTP: an infinite redirect loop (Cloudflare error 1000-series /
  # ERR_TOO_MANY_REDIRECTS). So the redirect must be switched off in this mode,
  # not merely left unset — ingress-nginx defaults ssl-redirect to true as soon
  # as a certificate is present anywhere in the class.
  edge_annotations = var.tls_mode == "edge" && local.nginx ? merge(
    {
      "nginx.ingress.kubernetes.io/ssl-redirect"       = "false"
      "nginx.ingress.kubernetes.io/force-ssl-redirect" = "false"
    },
    length(var.edge_source_ranges) > 0 ? {
      # Without this the origin is reachable over plain HTTP by anything that
      # can route to the Ingress, bypassing the edge entirely.
      "nginx.ingress.kubernetes.io/whitelist-source-range" = join(",", var.edge_source_ranges)
    } : {},
  ) : {}
}

resource "kubernetes_namespace" "beholdr" {
  metadata {
    name   = var.namespace
    labels = local.labels
  }
}

resource "kubernetes_service_account" "beholdr" {
  metadata {
    name      = local.name
    namespace = kubernetes_namespace.beholdr.metadata[0].name
    labels    = local.labels
  }
}

# Read-only access to everything Beholdr inspects, cluster-wide.
resource "kubernetes_cluster_role" "beholdr" {
  metadata {
    name   = local.name
    labels = local.labels
  }
  rule {
    api_groups = [""]
    resources  = ["nodes", "pods", "namespaces"]
    verbs      = ["get", "list", "watch"]
  }
  rule {
    api_groups = ["apps"]
    resources  = ["deployments", "replicasets", "statefulsets", "daemonsets"]
    verbs      = ["get", "list", "watch"]
  }
  rule {
    api_groups = ["autoscaling"]
    resources  = ["horizontalpodautoscalers"]
    verbs      = ["get", "list", "watch"]
  }
  rule {
    api_groups = ["metrics.k8s.io"]
    resources  = ["nodes", "pods"]
    verbs      = ["get", "list"]
  }
}

resource "kubernetes_cluster_role_binding" "beholdr" {
  metadata {
    name   = local.name
    labels = local.labels
  }
  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "ClusterRole"
    name      = kubernetes_cluster_role.beholdr.metadata[0].name
  }
  subject {
    kind      = "ServiceAccount"
    name      = kubernetes_service_account.beholdr.metadata[0].name
    namespace = kubernetes_namespace.beholdr.metadata[0].name
  }
}

resource "kubernetes_deployment" "beholdr" {
  metadata {
    name      = local.name
    namespace = kubernetes_namespace.beholdr.metadata[0].name
    labels    = local.labels
  }
  spec {
    replicas = var.replicas
    selector { match_labels = local.labels }
    template {
      metadata { labels = local.labels }
      spec {
        service_account_name = kubernetes_service_account.beholdr.metadata[0].name
        container {
          name  = local.name
          image = var.image
          port { container_port = 8000 }

          env {
            name  = "BEHOLDR_POLL_INTERVAL"
            value = tostring(var.poll_interval_seconds)
          }
          env {
            name  = "BEHOLDR_HISTORY_SIZE"
            value = tostring(var.history_size)
          }
          env {
            name  = "BEHOLDR_NAMESPACES"
            value = join(",", var.watch_namespaces)
          }
          env {
            name  = "BEHOLDR_KUBE_MODE"
            value = "in-cluster"
          }
          env {
            # No default: Beholdr exposes cluster topology, pod names, and
            # resource usage, so cross-origin access must be opted into
            # explicitly.
            name  = "BEHOLDR_CORS_ORIGINS"
            value = join(",", var.cors_origins)
          }
          env {
            name  = "BEHOLDR_PROMETHEUS_URL"
            value = var.prometheus_url
          }
          dynamic "env" {
            for_each = local.ca_file.prometheus != "" ? [1] : []
            content {
              name  = "BEHOLDR_PROMETHEUS_CA_FILE"
              value = local.ca_file.prometheus
            }
          }
          env {
            name  = "BEHOLDR_PROMETHEUS_TLS_INSECURE"
            value = tostring(var.prometheus_tls_insecure)
          }
          dynamic "env" {
            for_each = var.prometheus_bearer_token_secret_name != "" ? [1] : []
            content {
              name = "BEHOLDR_PROMETHEUS_BEARER_TOKEN"
              value_from {
                secret_key_ref {
                  name = var.prometheus_bearer_token_secret_name
                  key  = var.prometheus_bearer_token_secret_key
                }
              }
            }
          }
          env {
            name  = "BEHOLDR_ELASTICSEARCH_URL"
            value = var.elasticsearch_url
          }
          dynamic "env" {
            for_each = local.ca_file.elasticsearch != "" ? [1] : []
            content {
              name  = "BEHOLDR_ELASTICSEARCH_CA_FILE"
              value = local.ca_file.elasticsearch
            }
          }
          env {
            name  = "BEHOLDR_ELASTICSEARCH_TLS_INSECURE"
            value = tostring(var.elasticsearch_tls_insecure)
          }
          dynamic "env" {
            for_each = var.elasticsearch_api_key_secret_name != "" ? [1] : []
            content {
              name = "BEHOLDR_ELASTICSEARCH_API_KEY"
              value_from {
                secret_key_ref {
                  name = var.elasticsearch_api_key_secret_name
                  key  = var.elasticsearch_api_key_secret_key
                }
              }
            }
          }
          env {
            name  = "BEHOLDR_OTEL_COLLECTOR_HEALTH_URL"
            value = var.otel_collector_health_url
          }
          dynamic "env" {
            for_each = local.ca_file.otel != "" ? [1] : []
            content {
              name  = "BEHOLDR_OTEL_COLLECTOR_CA_FILE"
              value = local.ca_file.otel
            }
          }
          env {
            name  = "BEHOLDR_OTEL_COLLECTOR_TLS_INSECURE"
            value = tostring(var.otel_collector_tls_insecure)
          }
          env {
            name  = "BEHOLDR_INTEGRATION_CHECK_INTERVAL"
            value = tostring(var.integration_check_interval_seconds)
          }
          env {
            name  = "BEHOLDR_INTEGRATION_REQUEST_TIMEOUT"
            value = tostring(var.integration_request_timeout_seconds)
          }
          env {
            # Range and instant queries are real Prometheus work, not a
            # liveness probe: they must not inherit the health-check timeout.
            name  = "BEHOLDR_PROMETHEUS_QUERY_TIMEOUT"
            value = tostring(var.prometheus_query_timeout_seconds)
          }
          env {
            name  = "BEHOLDR_SERVICE_METRICS_CACHE_TTL"
            value = tostring(var.service_metrics_cache_ttl_seconds)
          }
          env {
            name  = "BEHOLDR_SERVICE_MAX_CONCURRENT_QUERIES"
            value = tostring(var.service_max_concurrent_queries)
          }
          dynamic "env" {
            for_each = { for name, value in local.service_health_env : name => value if value != "" }
            content {
              name  = env.key
              value = env.value
            }
          }

          dynamic "volume_mount" {
            for_each = local.ca_mounted ? [1] : []
            content {
              name       = "integration-ca"
              mount_path = local.ca_mount_path
              read_only  = true
            }
          }

          resources {
            requests = { cpu = "50m", memory = "128Mi" }
            limits   = { cpu = "500m", memory = "512Mi" }
          }

          liveness_probe {
            # Liveness only proves the process can serve HTTP; it must not
            # depend on collector/cluster state.
            http_get {
              path = "/live"
              port = 8000
            }
            initial_delay_seconds = 15
            period_seconds        = 20
          }
          readiness_probe {
            # Readiness fails until the collector has completed a recent
            # successful poll, so the Service won't route traffic here
            # before there's real data (or once that data goes stale).
            http_get {
              path = "/ready"
              port = 8000
            }
            initial_delay_seconds = 5
            period_seconds        = 10
          }
        }

        dynamic "volume" {
          for_each = local.ca_mounted ? [1] : []
          content {
            name = "integration-ca"
            secret {
              secret_name  = var.integration_ca_secret_name
              default_mode = "0444"
            }
          }
        }
      }
    }
  }

  lifecycle {
    precondition {
      condition     = var.prometheus_url != "" || alltrue([for value in values(local.service_health_env) : value == ""])
      error_message = "Service-health metric/label/threshold settings were supplied without prometheus_url, so nothing would query them. Set prometheus_url, or remove the service_* settings."
    }
    precondition {
      condition     = var.integration_request_timeout_seconds <= var.integration_check_interval_seconds
      error_message = "integration_request_timeout_seconds must not exceed integration_check_interval_seconds, or a slow backend is still in flight when the next check falls due."
    }
    precondition {
      condition     = local.ca_mounted || (var.prometheus_ca_secret_key == "" && var.elasticsearch_ca_secret_key == "" && var.otel_collector_ca_secret_key == "")
      error_message = "A *_ca_secret_key was set without integration_ca_secret_name, so no CA bundle would be mounted and the key would be silently ignored. Set integration_ca_secret_name to the Secret holding those bundles."
    }
  }
}

resource "kubernetes_service" "beholdr" {
  metadata {
    name      = local.name
    namespace = kubernetes_namespace.beholdr.metadata[0].name
    labels    = local.labels
  }
  spec {
    selector = local.labels
    port {
      name        = "http"
      port        = 80
      target_port = 8000
    }
  }
}

resource "kubernetes_ingress_v1" "beholdr" {
  count = var.ingress_enabled ? 1 : 0
  metadata {
    name      = local.name
    namespace = kubernetes_namespace.beholdr.metadata[0].name
    labels    = local.labels
    # Mode defaults first so an operator's own annotations still win.
    annotations = merge(local.edge_annotations, var.ingress_annotations, var.auth_annotations)
  }
  spec {
    ingress_class_name = var.ingress_class

    # Edge mode deliberately emits no tls block: the certificate lives at the
    # terminator (Cloudflare, an external LB, a mesh gateway), so the cluster
    # needs no key material of its own.
    dynamic "tls" {
      for_each = var.tls_mode == "cluster" && var.tls_secret_name != "" ? [1] : []
      content {
        hosts       = [var.ingress_host]
        secret_name = var.tls_secret_name
      }
    }

    rule {
      host = var.ingress_host
      http {
        path {
          path      = "/"
          path_type = "Prefix"
          backend {
            service {
              name = kubernetes_service.beholdr.metadata[0].name
              port { number = 80 }
            }
          }
        }
      }
    }
  }

  lifecycle {
    precondition {
      condition     = var.tls_mode != "cluster" || var.tls_secret_name != "" || var.insecure_http
      error_message = "Beholdr exposes cluster topology, pod names, and resource usage; the Ingress must use TLS. Set tls_secret_name (e.g. a cert-manager-issued secret), or set tls_mode = \"edge\" if TLS is terminated in front of the cluster (Cloudflare, an external load balancer), or explicitly set insecure_http = true to override for a non-production environment."
    }
    precondition {
      condition     = var.tls_mode != "edge" || trimspace(var.edge_tls_terminator) != ""
      error_message = "tls_mode = \"edge\" hands responsibility for TLS to something outside this module, so it must be named: set edge_tls_terminator (e.g. \"cloudflare\") to record what terminates it. That name is the audit trail for why this Ingress has no certificate."
    }
    precondition {
      condition     = var.tls_mode != "edge" || var.tls_secret_name == ""
      error_message = "tls_mode = \"edge\" means the certificate lives at the terminator, so tls_secret_name is ignored and must be empty. If the edge should instead speak HTTPS to this origin (Cloudflare Full (strict) with a Cloudflare Origin CA certificate), keep tls_mode = \"cluster\" and put that certificate in tls_secret_name."
    }
    precondition {
      condition     = length(var.auth_annotations) > 0 || var.insecure_no_auth
      error_message = "Beholdr has no built-in authentication. Set auth_annotations to point at an OIDC-aware auth proxy (e.g. oauth2-proxy via nginx.ingress.kubernetes.io/auth-url + auth-signin) or explicitly set insecure_no_auth = true to override for a non-production environment."
    }
  }
}
