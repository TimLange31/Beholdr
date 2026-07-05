output "namespace" {
  value = kubernetes_namespace.beholdr.metadata[0].name
}

output "service" {
  value = "${kubernetes_service.beholdr.metadata[0].name}.${kubernetes_namespace.beholdr.metadata[0].name}.svc.cluster.local"
}

output "url" {
  value = var.ingress_enabled ? "http://${var.ingress_host}/" : "port-forward: kubectl -n ${var.namespace} port-forward svc/beholdr 8000:80"
}
