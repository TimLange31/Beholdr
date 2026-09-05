// Package k8s wraps client-go with exactly the reads Beholdr needs. Every CPU
// value is returned in millicores and every memory value in bytes, so callers
// never touch resource.Quantity suffixes.
package k8s

import (
	"context"
	"fmt"
	"log/slog"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type Client struct {
	cs         *kubernetes.Clientset
	metrics    *metricsclient.Clientset
	namespaces []string
	log        *slog.Logger

	MetricsAvailable bool
}

// New builds a client. mode is "auto" | "in-cluster" | "kubeconfig".
func New(mode, kubeconfig string, namespaces []string, log *slog.Logger) (*Client, error) {
	cfg, err := restConfig(mode, kubeconfig, log)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes clientset: %w", err)
	}
	mc, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("metrics clientset: %w", err)
	}
	return &Client{cs: cs, metrics: mc, namespaces: namespaces, log: log, MetricsAvailable: true}, nil
}

func restConfig(mode, kubeconfig string, log *slog.Logger) (*rest.Config, error) {
	if mode == "auto" || mode == "in-cluster" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			log.Info("using in-cluster config")
			return cfg, nil
		} else if mode == "in-cluster" {
			return nil, fmt.Errorf("in-cluster config: %w", err)
		}
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubeconfig: %w", err)
	}
	log.Info("using local kubeconfig")
	return cfg, nil
}

// --- listing ---------------------------------------------------------------

func (c *Client) Nodes(ctx context.Context) ([]corev1.Node, error) {
	l, err := c.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (c *Client) Pods(ctx context.Context) ([]corev1.Pod, error) {
	if len(c.namespaces) == 0 {
		l, err := c.cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return l.Items, nil
	}
	var out []corev1.Pod
	for _, ns := range c.namespaces {
		l, err := c.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, l.Items...)
	}
	return out, nil
}

func (c *Client) Deployments(ctx context.Context) ([]appsv1.Deployment, error) {
	return listNamespaced(c.namespaces, func(ns string) ([]appsv1.Deployment, error) {
		l, err := c.cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return l.Items, nil
	})
}

func (c *Client) StatefulSets(ctx context.Context) ([]appsv1.StatefulSet, error) {
	return listNamespaced(c.namespaces, func(ns string) ([]appsv1.StatefulSet, error) {
		l, err := c.cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return l.Items, nil
	})
}

func (c *Client) DaemonSets(ctx context.Context) ([]appsv1.DaemonSet, error) {
	return listNamespaced(c.namespaces, func(ns string) ([]appsv1.DaemonSet, error) {
		l, err := c.cs.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		return l.Items, nil
	})
}

// listNamespaced runs list once against "" (all namespaces) when the client
// isn't restricted to a specific set, otherwise once per configured
// namespace, concatenating the results.
func listNamespaced[T any](namespaces []string, list func(ns string) ([]T, error)) ([]T, error) {
	if len(namespaces) <= 1 {
		ns := ""
		if len(namespaces) == 1 {
			ns = namespaces[0]
		}
		return list(ns)
	}
	var out []T
	for _, n := range namespaces {
		items, err := list(n)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	return out, nil
}

func (c *Client) HPAs(ctx context.Context) ([]autoscalingv1.HorizontalPodAutoscaler, error) {
	l, err := c.cs.AutoscalingV1().HorizontalPodAutoscalers("").List(ctx, metav1.ListOptions{})
	if err != nil {
		c.log.Warn("hpa list failed", "err", err)
		return nil, nil // non-fatal
	}
	return l.Items, nil
}

// --- metrics.k8s.io --------------------------------------------------------

type Usage struct{ CPUMilli, MemBytes int64 }

func (c *Client) NodeMetrics(ctx context.Context) map[string]Usage {
	out := map[string]Usage{}
	l, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		c.MetricsAvailable = false
		c.log.Warn("node metrics unavailable (metrics-server?)", "err", err)
		return out
	}
	for _, m := range l.Items {
		out[m.Name] = Usage{
			CPUMilli: m.Usage.Cpu().MilliValue(),
			MemBytes: m.Usage.Memory().Value(),
		}
	}
	return out
}

// PodMetrics keys by "namespace/name", summed over containers.
func (c *Client) PodMetrics(ctx context.Context) map[string]Usage {
	out := map[string]Usage{}
	l, err := c.metrics.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		c.MetricsAvailable = false
		c.log.Warn("pod metrics unavailable (metrics-server?)", "err", err)
		return out
	}
	for _, m := range l.Items {
		var u Usage
		for _, ct := range m.Containers {
			u.CPUMilli += ct.Usage.Cpu().MilliValue()
			u.MemBytes += ct.Usage.Memory().Value()
		}
		out[m.Namespace+"/"+m.Name] = u
	}
	return out
}
