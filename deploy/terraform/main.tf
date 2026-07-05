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

          resources {
            requests = { cpu = "50m", memory = "128Mi" }
            limits   = { cpu = "500m", memory = "512Mi" }
          }

          liveness_probe {
            http_get {
              path = "/api/health"
              port = 8000
            }
            initial_delay_seconds = 15
            period_seconds        = 20
          }
          readiness_probe {
            http_get {
              path = "/api/health"
              port = 8000
            }
            initial_delay_seconds = 5
            period_seconds        = 10
          }
        }
      }
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
    name        = local.name
    namespace   = kubernetes_namespace.beholdr.metadata[0].name
    labels      = local.labels
    annotations = var.ingress_annotations
  }
  spec {
    ingress_class_name = var.ingress_class
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
}
