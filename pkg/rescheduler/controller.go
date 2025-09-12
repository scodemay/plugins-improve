package rescheduler

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	metricsClient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	// 控制器配置
	DefaultReschedulingInterval = 30 * time.Second
	DefaultImbalanceThreshold   = 20.0 // 负载不均衡阈值
	MaxReschedulingPods         = 10   // 单次最大重调度Pod数量

	// 重调度原因
	ReasonLoadBalancing        = "LoadBalancing"
	ReasonResourceOptimization = "ResourceOptimization"
	ReasonNodeMaintenance      = "NodeMaintenance"
)



// ReschedulingController 重调度控制器
type ReschedulingController struct {
	// 基础组件
	logger        klog.Logger
	kubeClient    kubernetes.Interface
	metricsClient metricsClient.Interface
	podLister     corelisters.PodLister
	nodeLister    corelisters.NodeLister

	// 配置
	config *ReschedulerConfig

	// 资源计算器 - 统一资源计算逻辑
	resourceCalculator *ResourceCalculator

	// 控制器状态
	stopCh   chan struct{}
	interval time.Duration

	// 并发控制
	mutex sync.RWMutex

	// 重调度历史（避免频繁重调度）
	reschedulingHistory map[string]time.Time
}

// ReschedulingDecision 重调度决策
type ReschedulingDecision struct {
	Pod        *v1.Pod
	SourceNode string
	TargetNode string
	Reason     string
	Strategy   string
}

// NodeMetrics 节点真实使用率指标
type NodeMetrics struct {
	NodeName       string
	CPUUsage       resource.Quantity // 实际CPU使用量
	MemoryUsage    resource.Quantity // 实际内存使用量
	CPUCapacity    resource.Quantity // CPU容量
	MemoryCapacity resource.Quantity // 内存容量
	CPUPercent     float64           // CPU使用率百分比
	MemoryPercent  float64           // 内存使用率百分比
}

// NewReschedulingController 创建重调度控制器
func NewReschedulingController(
	kubeClient kubernetes.Interface,
	metricsClient metricsClient.Interface,
	podLister corelisters.PodLister,
	nodeLister corelisters.NodeLister,
	config *ReschedulerConfig,
) *ReschedulingController {
	logger := klog.Background().WithName("rescheduling-controller")

	return &ReschedulingController{
		logger:              logger,
		kubeClient:          kubeClient,
		metricsClient:       metricsClient,
		podLister:           podLister,
		nodeLister:          nodeLister,
		config:              config,
		resourceCalculator:  NewResourceCalculator(config),
		stopCh:              make(chan struct{}),
		interval:            DefaultReschedulingInterval,
		reschedulingHistory: make(map[string]time.Time),
	}
}

// Run 启动控制器
func (c *ReschedulingController) Run(ctx context.Context) error {
	c.logger.Info("启动重调度控制器", "interval", c.interval)

	// 等待缓存同步
	c.logger.Info("等待缓存同步...")

	// 启动主循环
	wait.Until(func() {
		if err := c.performRescheduling(ctx); err != nil {
			c.logger.Error(err, "重调度过程中发生错误")
		}
	}, c.interval, c.stopCh)

	return nil
}

// Stop 停止控制器
func (c *ReschedulingController) Stop() {
	c.logger.Info("停止重调度控制器")
	close(c.stopCh)
}

// ============================= 核心重调度逻辑 =============================

// performRescheduling 执行重调度逻辑
func (c *ReschedulingController) performRescheduling(ctx context.Context) error {
	c.logger.V(2).Info("开始执行重调度检查")

	// 1. 获取节点真实使用率（通过Metrics API）
	nodeMetrics, err := c.getNodeMetrics(ctx)
	if err != nil {
		return fmt.Errorf("获取节点指标失败: %v", err)
	}

	if len(nodeMetrics) == 0 {
		c.logger.V(2).Info("没有可用的节点指标数据")
		return nil
	}

	// 2. 分析负载不均衡情况
	decisions := c.analyzeAndDecide(ctx, nodeMetrics)
	if len(decisions) == 0 {
		c.logger.V(2).Info("没有需要重调度的Pod")
		return nil
	}

	// 3. 执行重调度决策
	c.logger.Info("开始执行重调度决策", "决策数量", len(decisions))

	successCount := 0
	for i, decision := range decisions {
		if i >= MaxReschedulingPods {
			c.logger.Info("达到最大重调度数量限制", "limit", MaxReschedulingPods)
			break
		}

		if err := c.executeMigration(ctx, decision); err != nil {
			c.logger.Error(err, "重调度失败",
				"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
				"reason", decision.Reason)
		} else {
			successCount++
			c.markRescheduled(decision.Pod.Name)
		}
	}

	if successCount > 0 {
		c.logger.Info("重调度执行完成", "成功数量", successCount, "总决策数", len(decisions))
	}

	return nil
}

// getNodeMetrics 获取节点真实使用率指标
func (c *ReschedulingController) getNodeMetrics(ctx context.Context) ([]NodeMetrics, error) {
	// 从 Metrics Server 获取节点指标
	nodeMetricsList, err := c.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取节点指标失败: %v", err)
	}

	// 获取节点列表
	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("获取节点列表失败: %v", err)
	}

	// 构建节点容量映射
	nodeCapacity := make(map[string]*v1.Node)
	for _, node := range nodes {
		if !node.Spec.Unschedulable {
			nodeCapacity[node.Name] = node
		}
	}

	var result []NodeMetrics
	for _, nodeMetric := range nodeMetricsList.Items {
		node, exists := nodeCapacity[nodeMetric.Name]
		if !exists {
			continue
		}

		// 使用统一的资源计算逻辑
		usage := c.resourceCalculator.CalculateUsageFromMetrics(
			nodeMetric.Usage.Cpu().DeepCopy(),
			nodeMetric.Usage.Memory().DeepCopy(),
			node.Status.Capacity.Cpu().DeepCopy(),
			node.Status.Capacity.Memory().DeepCopy(),
		)

		result = append(result, NodeMetrics{
			NodeName:       nodeMetric.Name,
			CPUUsage:       nodeMetric.Usage.Cpu().DeepCopy(),
			MemoryUsage:    nodeMetric.Usage.Memory().DeepCopy(),
			CPUCapacity:    node.Status.Capacity.Cpu().DeepCopy(),
			MemoryCapacity: node.Status.Capacity.Memory().DeepCopy(),
			CPUPercent:     usage.CPU,
			MemoryPercent:  usage.Memory,
		})
	}

	c.logger.V(2).Info("获取到节点指标", "节点数量", len(result))
	return result, nil
}

// analyzeAndDecide 分析节点负载并做出重调度决策
func (c *ReschedulingController) analyzeAndDecide(ctx context.Context, metrics []NodeMetrics) []ReschedulingDecision {
	var decisions []ReschedulingDecision

	// 1. 负载均衡分析
	loadBalanceDecisions := c.analyzeLoadBalance(ctx, metrics)
	decisions = append(decisions, loadBalanceDecisions...)

	// 2. 资源优化分析
	resourceOptDecisions := c.analyzeResourceOptimization(ctx, metrics)
	decisions = append(decisions, resourceOptDecisions...)

	// 3. 节点维护分析
	maintenanceDecisions := c.analyzeNodeMaintenance(ctx, metrics)
	decisions = append(decisions, maintenanceDecisions...)

	return decisions
}

// analyzeLoadBalance 负载均衡分析
func (c *ReschedulingController) analyzeLoadBalance(ctx context.Context, metrics []NodeMetrics) []ReschedulingDecision {
	if len(metrics) < 2 {
		return nil
	}

	// 按CPU使用率排序
	sortedMetrics := make([]NodeMetrics, len(metrics))
	copy(sortedMetrics, metrics)
	sort.Slice(sortedMetrics, func(i, j int) bool {
		return sortedMetrics[i].CPUPercent > sortedMetrics[j].CPUPercent
	})

	highest := sortedMetrics[0]
	lowest := sortedMetrics[len(sortedMetrics)-1]

	// 检查负载不均衡
	imbalance := highest.CPUPercent - lowest.CPUPercent
	if imbalance < DefaultImbalanceThreshold {
		c.logger.V(3).Info("节点负载均衡良好",
			"imbalance", imbalance,
			"threshold", DefaultImbalanceThreshold)
		return nil
	}

	c.logger.Info("发现负载不均衡",
		"highNode", highest.NodeName, "highCPU", highest.CPUPercent,
		"lowNode", lowest.NodeName, "lowCPU", lowest.CPUPercent,
		"imbalance", imbalance)

	// 找到可以从高负载节点迁移的Pod
	candidatePods := c.findMigratablePods(ctx, highest.NodeName)
	if len(candidatePods) == 0 {
		return nil
	}

	var decisions []ReschedulingDecision
	// 选择1-2个Pod进行迁移
	migrateCount := min(2, len(candidatePods))
	for i := 0; i < migrateCount; i++ {
		decisions = append(decisions, ReschedulingDecision{
			Pod:        candidatePods[i],
			SourceNode: highest.NodeName,
			TargetNode: lowest.NodeName,
			Reason:     fmt.Sprintf("负载均衡：源节点CPU %.1f%% → 目标节点CPU %.1f%%", highest.CPUPercent, lowest.CPUPercent),
			Strategy:   ReasonLoadBalancing,
		})
	}

	return decisions
}

// analyzeResourceOptimization 资源优化分析
func (c *ReschedulingController) analyzeResourceOptimization(ctx context.Context, metrics []NodeMetrics) []ReschedulingDecision {
	var decisions []ReschedulingDecision

	for _, metric := range metrics {
		// 使用统一的过载检查逻辑
		usage := ResourceUsagePercent{
			CPU:    metric.CPUPercent,
			Memory: metric.MemoryPercent,
		}

		isOverloaded, reason := c.resourceCalculator.IsOverloaded(usage)
		if !isOverloaded {
			continue
		}

		c.logger.Info("发现过载节点",
			"node", metric.NodeName,
			"cpuUsage", metric.CPUPercent,
			"memoryUsage", metric.MemoryPercent,
			"reason", reason)

		// 查找可迁移的Pod
		candidatePods := c.findMigratablePods(ctx, metric.NodeName)
		if len(candidatePods) == 0 {
			continue
		}

		// 查找最佳目标节点
		targetNode := c.findBestTargetNode(metrics, metric.NodeName)
		if targetNode == "" {
			continue
		}

		// 创建重调度决策
		decisions = append(decisions, ReschedulingDecision{
			Pod:        candidatePods[0], // 选择第一个Pod
			SourceNode: metric.NodeName,
			TargetNode: targetNode,
			Reason:     fmt.Sprintf("资源优化：节点CPU %.1f%% 内存 %.1f%% 超过阈值", metric.CPUPercent, metric.MemoryPercent),
			Strategy:   ReasonResourceOptimization,
		})

		// 限制每个节点最多迁移1个Pod
		break
	}

	return decisions
}

// analyzeNodeMaintenance 节点维护分析
func (c *ReschedulingController) analyzeNodeMaintenance(ctx context.Context, metrics []NodeMetrics) []ReschedulingDecision {
	var decisions []ReschedulingDecision

	nodes, err := c.nodeLister.List(labels.Everything())
	if err != nil {
		c.logger.Error(err, "获取节点列表失败")
		return nil
	}

	for _, node := range nodes {
		// 检查维护标签
		if value, exists := node.Labels["scheduler.alpha.kubernetes.io/maintenance"]; !exists || value != "true" {
			continue
		}

		c.logger.Info("发现维护模式节点", "node", node.Name)

		// 查找该节点上的所有Pod
		candidatePods := c.findMigratablePods(ctx, node.Name)

		// 为每个Pod找目标节点
		for _, pod := range candidatePods {
			targetNode := c.findBestTargetNode(metrics, node.Name)
			if targetNode == "" {
				continue
			}

			decisions = append(decisions, ReschedulingDecision{
				Pod:        pod,
				SourceNode: node.Name,
				TargetNode: targetNode,
				Reason:     "节点维护模式",
				Strategy:   ReasonNodeMaintenance,
			})
		}
	}

	return decisions
}



// PodFilter Pod筛选器 - 统一Pod筛选逻辑
type PodFilter struct {
	controller *ReschedulingController
}

// NewPodFilter 创建Pod筛选器
func (c *ReschedulingController) NewPodFilter() *PodFilter {
	return &PodFilter{controller: c}
}

// IsMigratable 检查Pod是否可以迁移
func (pf *PodFilter) IsMigratable(pod *v1.Pod, targetNodeName string) bool {
	c := pf.controller

	// 基本筛选条件
	if pod.Spec.NodeName != targetNodeName || pod.Status.Phase != v1.PodRunning {
		return false
	}

	// 排除系统命名空间
	if c.isExcludedNamespace(pod.Namespace) {
		return false
	}

	// 排除DaemonSet Pod
	if c.isDaemonSetPod(pod) {
		return false
	}

	// 排除静态Pod
	if c.isStaticPod(pod) {
		return false
	}

	// 检查是否最近已经被重调度过
	if c.isRecentlyRescheduled(pod.Name) {
		return false
	}

	return true
}

// SortByPriority 按优先级排序Pod（低优先级先迁移）
func (pf *PodFilter) SortByPriority(pods []*v1.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		pi := int32(0)
		if pods[i].Spec.Priority != nil {
			pi = *pods[i].Spec.Priority
		}
		pj := int32(0)
		if pods[j].Spec.Priority != nil {
			pj = *pods[j].Spec.Priority
		}
		return pi < pj
	})
}

// findMigratablePods 查找可以迁移的Pod - 使用统一的筛选器
func (c *ReschedulingController) findMigratablePods(ctx context.Context, nodeName string) []*v1.Pod {
	_ = ctx // 当前未使用，但保留以备将来需要

	pods, err := c.podLister.List(labels.Everything())
	if err != nil {
		c.logger.Error(err, "获取Pod列表失败")
		return nil
	}

	// 使用统一的Pod筛选器
	filter := c.NewPodFilter()
	var candidates []*v1.Pod

	for _, pod := range pods {
		if filter.IsMigratable(pod, nodeName) {
			candidates = append(candidates, pod)
		}
	}

	// 使用统一的排序逻辑
	filter.SortByPriority(candidates)

	return candidates
}

// executeMigration 执行Pod迁移
func (c *ReschedulingController) executeMigration(ctx context.Context, decision ReschedulingDecision) error {
	c.logger.Info("执行Pod迁移",
		"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
		"from", decision.SourceNode,
		"to", decision.TargetNode,
		"reason", decision.Reason)

	// 使用Eviction API驱逐Pod
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      decision.Pod.Name,
			Namespace: decision.Pod.Namespace,
		},
		DeleteOptions: &metav1.DeleteOptions{
			GracePeriodSeconds: &[]int64{30}[0],
		},
	}

	err := c.kubeClient.PolicyV1().Evictions(decision.Pod.Namespace).Evict(ctx, eviction)
	if err != nil {
		if errors.IsTooManyRequests(err) {
			return fmt.Errorf("驱逐被限流，稍后重试: %v", err)
		}
		return fmt.Errorf("驱逐Pod失败: %v", err)
	}

	c.logger.Info("成功驱逐Pod", "pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name))
	return nil
}


func (c *ReschedulingController) isExcludedNamespace(namespace string) bool {
	for _, excluded := range c.config.ExcludedNamespaces {
		if namespace == excluded {
			return true
		}
	}
	return false
}

func (c *ReschedulingController) isDaemonSetPod(pod *v1.Pod) bool {
	for _, ownerRef := range pod.OwnerReferences {
		if ownerRef.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func (c *ReschedulingController) isStaticPod(pod *v1.Pod) bool {
	return pod.Annotations["kubernetes.io/config.source"] == "api" ||
		pod.Annotations["kubernetes.io/config.source"] == "file"
}

func (c *ReschedulingController) markRescheduled(podName string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.reschedulingHistory[podName] = time.Now()
}

func (c *ReschedulingController) isRecentlyRescheduled(podName string) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if lastTime, exists := c.reschedulingHistory[podName]; exists {
		return time.Since(lastTime) < 10*time.Minute // 10分钟内不重复重调度
	}
	return false
}

func (c *ReschedulingController) findBestTargetNode(metrics []NodeMetrics, excludeNode string) string {
	var bestNode string
	bestScore := float64(-1)

	for _, metric := range metrics {
		if metric.NodeName == excludeNode {
			continue
		}

		// 使用统一的评分逻辑
		score := c.resourceCalculator.CalculateNodeScoreFromMetrics(metric.CPUPercent, metric.MemoryPercent)
		if score > bestScore {
			bestScore = score
			bestNode = metric.NodeName
		}
	}

	return bestNode
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
