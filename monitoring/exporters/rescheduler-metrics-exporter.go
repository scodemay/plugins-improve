package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclientset "k8s.io/metrics/pkg/client/clientset/versioned"
)

var (
	// Pod分布方差指标
	podDistributionVariance = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rescheduler_pod_distribution_variance",
			Help: "Variance of pod distribution across nodes",
		},
		[]string{"resource_type"}, // cpu, memory, count
	)

	// 节点负载均衡度
	nodeLoadBalanceScore = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rescheduler_load_balance_score",
			Help: "Load balance score (100 - standard_deviation)",
		},
		[]string{"resource_type"}, // cpu, memory
	)

	// 每个节点的Pod数量
	nodePodsCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rescheduler_node_pods_count",
			Help: "Number of pods per node",
		},
		[]string{"node_name", "pod_type"}, // service, job, all
	)

	// 每个节点的资源使用率
	nodeResourceUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rescheduler_node_resource_usage_percent",
			Help: "Resource usage percentage per node",
		},
		[]string{"node_name", "resource_type"}, // cpu, memory
	)

	// 重调度事件计数器
	reschedulingEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rescheduler_events_total",
			Help: "Total number of rescheduling events",
		},
		[]string{"event_type", "reason"}, // migration_started, migration_completed, migration_failed
	)

	// 调度延迟
	schedulingLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rescheduler_scheduling_duration_seconds",
			Help:    "Time taken to schedule a pod",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"scheduler_name"},
	)
)

type MetricsExporter struct {
	kubeClient    kubernetes.Interface
	metricsClient metricsclientset.Interface
}

func init() {
	prometheus.MustRegister(podDistributionVariance)
	prometheus.MustRegister(nodeLoadBalanceScore)
	prometheus.MustRegister(nodePodsCount)
	prometheus.MustRegister(nodeResourceUsage)
	prometheus.MustRegister(reschedulingEvents)
	prometheus.MustRegister(schedulingLatency)
}

func NewMetricsExporter() (*MetricsExporter, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %v", err)
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	metricsClient, err := metricsclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics client: %v", err)
	}

	return &MetricsExporter{
		kubeClient:    kubeClient,
		metricsClient: metricsClient,
	}, nil
}

func (m *MetricsExporter) collectMetrics(ctx context.Context) error {
	// 收集Pod分布数据
	if err := m.collectPodDistribution(ctx); err != nil {
		log.Printf("Failed to collect pod distribution: %v", err)
	}

	// 收集节点资源使用率
	if err := m.collectNodeResourceUsage(ctx); err != nil {
		log.Printf("Failed to collect node resource usage: %v", err)
	}

	return nil
}

func (m *MetricsExporter) collectPodDistribution(ctx context.Context) error {
	// 获取所有运行中的Pod（包括kube-system）
	pods, err := m.kubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %v", err)
	}

	// 统计每个节点的Pod数量
	nodePodCount := make(map[string]int)        // 总Pod数
	nodeServicePodCount := make(map[string]int) // 服务Pod数
	nodeJobPodCount := make(map[string]int)     // Job Pod数

	for _, pod := range pods.Items {
		// 移除kube-system过滤 - 现在监控所有Pod
		// if pod.Namespace == "kube-system" {
		//	continue
		// }

		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}

		nodePodCount[nodeName]++

		// 判断Pod类型
		isJobPod := false
		for _, ownerRef := range pod.OwnerReferences {
			if ownerRef.Kind == "Job" {
				isJobPod = true
				break
			}
		}

		if isJobPod {
			nodeJobPodCount[nodeName]++
		} else {
			nodeServicePodCount[nodeName]++
		}
	}

	// 更新Prometheus指标
	for nodeName, count := range nodePodCount {
		nodePodsCount.WithLabelValues(nodeName, "all").Set(float64(count))
	}
	for nodeName, count := range nodeServicePodCount {
		nodePodsCount.WithLabelValues(nodeName, "service").Set(float64(count))
	}
	for nodeName, count := range nodeJobPodCount {
		nodePodsCount.WithLabelValues(nodeName, "job").Set(float64(count))
	}

	// 计算分布方差
	allCounts := make([]float64, 0, len(nodePodCount))
	serviceCounts := make([]float64, 0, len(nodeServicePodCount))

	for _, count := range nodePodCount {
		allCounts = append(allCounts, float64(count))
	}
	for _, count := range nodeServicePodCount {
		serviceCounts = append(serviceCounts, float64(count))
	}

	if len(allCounts) > 0 {
		podDistributionVariance.WithLabelValues("count").Set(calculateVariance(allCounts))
	}
	if len(serviceCounts) > 0 {
		podDistributionVariance.WithLabelValues("service_count").Set(calculateVariance(serviceCounts))
	}

	return nil
}

func (m *MetricsExporter) collectNodeResourceUsage(ctx context.Context) error {
	// 获取节点指标
	nodeMetrics, err := m.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node metrics: %v", err)
	}

	// 获取节点容量信息
	nodes, err := m.kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %v", err)
	}

	nodeCapacity := make(map[string]*metricsv1beta1.NodeMetrics)
	for i := range nodeMetrics.Items {
		nodeCapacity[nodeMetrics.Items[i].Name] = &nodeMetrics.Items[i]
	}

	cpuUsages := make([]float64, 0, len(nodes.Items))
	memoryUsages := make([]float64, 0, len(nodes.Items))

	for _, node := range nodes.Items {
		if node.Spec.Unschedulable {
			continue
		}

		metrics, exists := nodeCapacity[node.Name]
		if !exists {
			continue
		}

		// 计算使用率
		cpuCapacity := node.Status.Capacity.Cpu().MilliValue()
		memoryCapacity := node.Status.Capacity.Memory().Value()

		cpuUsage := metrics.Usage.Cpu().MilliValue()
		memoryUsage := metrics.Usage.Memory().Value()

		cpuPercent := float64(cpuUsage) / float64(cpuCapacity) * 100
		memoryPercent := float64(memoryUsage) / float64(memoryCapacity) * 100

		// 更新单节点指标
		nodeResourceUsage.WithLabelValues(node.Name, "cpu").Set(cpuPercent)
		nodeResourceUsage.WithLabelValues(node.Name, "memory").Set(memoryPercent)

		cpuUsages = append(cpuUsages, cpuPercent)
		memoryUsages = append(memoryUsages, memoryPercent)
	}

	// 计算负载均衡分数 (100 - 标准差)
	if len(cpuUsages) > 0 {
		cpuStdDev := calculateStandardDeviation(cpuUsages)
		nodeLoadBalanceScore.WithLabelValues("cpu").Set(100 - cpuStdDev)
	}
	if len(memoryUsages) > 0 {
		memoryStdDev := calculateStandardDeviation(memoryUsages)
		nodeLoadBalanceScore.WithLabelValues("memory").Set(100 - memoryStdDev)
	}

	return nil
}

func calculateVariance(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))

	variance := 0.0
	for _, v := range values {
		variance += math.Pow(v-mean, 2)
	}
	variance /= float64(len(values))

	return variance
}

func calculateStandardDeviation(values []float64) float64 {
	return math.Sqrt(calculateVariance(values))
}

func main() {
	exporter, err := NewMetricsExporter()
	if err != nil {
		log.Fatalf("Failed to create metrics exporter: %v", err)
	}

	// 启动定期收集
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := exporter.collectMetrics(ctx); err != nil {
					log.Printf("Failed to collect metrics: %v", err)
				}
				cancel()
			}
		}
	}()

	// 启动HTTP服务器
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	log.Println("Starting metrics server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
