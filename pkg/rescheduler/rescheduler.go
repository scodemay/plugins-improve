package rescheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	clientset "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	// Name 是重调度器插件在注册表和配置中使用的名称
	Name = "Rescheduler"

	// 重调度策略
	LoadBalancingStrategy        = "LoadBalancing"
	ResourceOptimizationStrategy = "ResourceOptimization"
	NodeMaintenanceStrategy      = "NodeMaintenance"

	// 默认配置
	DefaultReschedulingInterval = 30 * time.Second
	DefaultCPUThreshold         = 10.0 // 降低到10%，更容易触发
	DefaultMemoryThreshold      = 20.0 // 降低到20%
	DefaultImbalanceThreshold   = 5.0  // 降低到5%，更敏感

	// 新增：调度优化默认配置
	DefaultCPUScoreWeight    = 0.6
	DefaultMemoryScoreWeight = 0.4
	DefaultLoadBalanceBonus  = 10.0
)

// ReschedulerConfig 重调度器配置
type ReschedulerConfig struct {
	// 重调度间隔
	ReschedulingInterval time.Duration `json:"reschedulingInterval,omitempty"`

	// 启用的策略
	EnabledStrategies []string `json:"enabledStrategies,omitempty"`

	// CPU使用率阈值 (%)
	CPUThreshold float64 `json:"cpuThreshold,omitempty"`

	// 内存使用率阈值 (%)
	MemoryThreshold float64 `json:"memoryThreshold,omitempty"`

	// 负载不均衡阈值 (%)
	ImbalanceThreshold float64 `json:"imbalanceThreshold,omitempty"`

	// 最大重调度Pod数量
	MaxReschedulePods int `json:"maxReschedulePods,omitempty"`

	// 排除的命名空间
	ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`

	// 排除的Pod标签选择器
	ExcludedPodSelector string `json:"excludedPodSelector,omitempty"`

	// 调度优化配置
	EnableSchedulingOptimization bool    `json:"enableSchedulingOptimization,omitempty"`
	EnablePreventiveRescheduling bool    `json:"enablePreventiveRescheduling,omitempty"`
	CPUScoreWeight               float64 `json:"cpuScoreWeight,omitempty"`
	MemoryScoreWeight            float64 `json:"memoryScoreWeight,omitempty"`
	LoadBalanceBonus             float64 `json:"loadBalanceBonus,omitempty"`
}

// Rescheduler 重调度器结构体
type Rescheduler struct {
	logger     klog.Logger
	handle     framework.Handle
	config     *ReschedulerConfig
	clientset  clientset.Interface
	podLister  corelisters.PodLister
	nodeLister corelisters.NodeLister

	// 重调度控制器 - 负责执行实际的Pod迁移
	controller *ReschedulerController

	// Deployment协调器 - 避免与Deployment Controller冲突
	deploymentCoordinator *DeploymentCoordinator

	// 停止信号
	stopCh chan struct{}

	// 预防性重调度缓存（并发安全）
	recentReschedulingTargets map[string]time.Time
	targetsMutex              sync.RWMutex
}

// ReschedulingDecision 重调度决策
type ReschedulingDecision struct {
	Pod        *v1.Pod `json:"pod"`
	SourceNode string  `json:"sourceNode"`
	TargetNode string  `json:"targetNode"`
	Reason     string  `json:"reason"`
	Strategy   string  `json:"strategy"`
}

// NodeResourceUsage 节点资源使用情况
type NodeResourceUsage struct {
	Node               *v1.Node
	CPUUsagePercent    float64
	MemoryUsagePercent float64
	CPURequests        int64 // 累计CPU请求量 (毫核)
	MemoryRequests     int64 // 累计内存请求量 (字节)
	PodCount           int
	Score              float64
}

// 确保Rescheduler实现了相关接口
var _ framework.Plugin = &Rescheduler{}
var _ framework.FilterPlugin = &Rescheduler{}
var _ framework.ScorePlugin = &Rescheduler{}
var _ framework.PreBindPlugin = &Rescheduler{}

// Name 返回插件名称
func (r *Rescheduler) Name() string {
	return Name
}

// New 初始化重调度器插件
func New(ctx context.Context, obj runtime.Object, h framework.Handle) (framework.Plugin, error) {
	logger := klog.FromContext(ctx).WithName("Rescheduler")
	logger.Info("重调度器插件正在初始化")

	// 解析配置
	config := &ReschedulerConfig{
		ReschedulingInterval:         DefaultReschedulingInterval,
		EnabledStrategies:            []string{LoadBalancingStrategy, ResourceOptimizationStrategy},
		CPUThreshold:                 DefaultCPUThreshold,
		MemoryThreshold:              DefaultMemoryThreshold,
		ImbalanceThreshold:           DefaultImbalanceThreshold,
		MaxReschedulePods:            10,
		ExcludedNamespaces:           []string{"kube-system", "kube-public"},
		EnableSchedulingOptimization: true,
		EnablePreventiveRescheduling: true,
		CPUScoreWeight:               DefaultCPUScoreWeight,
		MemoryScoreWeight:            DefaultMemoryScoreWeight,
		LoadBalanceBonus:             DefaultLoadBalanceBonus,
	}

	// 从obj中解析实际配置
	if obj != nil {
		if err := parsePluginConfig(obj, config, logger); err != nil {
			logger.Error(err, "解析插件配置失败，使用默认配置")
		}
	}

	// 创建重调度控制器
	controller := NewReschedulerController(
		h.ClientSet(),
		h.SharedInformerFactory(),
	)

	// 创建Deployment协调器
	deploymentCoordinator := NewDeploymentCoordinator(
		h.ClientSet(),
		h.SharedInformerFactory().Apps().V1().Deployments().Lister(),
		h.SharedInformerFactory().Apps().V1().ReplicaSets().Lister(),
		h.SharedInformerFactory().Core().V1().Pods().Lister(),
	)

	rescheduler := &Rescheduler{
		logger:                    logger,
		handle:                    h,
		config:                    config,
		clientset:                 h.ClientSet(),
		podLister:                 h.SharedInformerFactory().Core().V1().Pods().Lister(),
		nodeLister:                h.SharedInformerFactory().Core().V1().Nodes().Lister(),
		controller:                controller,
		deploymentCoordinator:     deploymentCoordinator,
		stopCh:                    make(chan struct{}),
		recentReschedulingTargets: make(map[string]time.Time),
	}

	// 启动重调度控制器
	go func() {
		if err := controller.Run(ctx, 2); err != nil {
			logger.Error(err, "重调度控制器运行失败")
		}
	}()

	// 启动重调度器控制循环
	go rescheduler.run(ctx)

	return rescheduler, nil
}

// run 运行重调度器主循环
func (r *Rescheduler) run(ctx context.Context) {
	r.logger.Info("重调度器开始运行", "interval", r.config.ReschedulingInterval)

	wait.Until(func() {
		if err := r.performRescheduling(ctx); err != nil {
			r.logger.Error(err, "重调度过程中发生错误")
		}
	}, r.config.ReschedulingInterval, r.stopCh)
}

// performRescheduling 执行重调度逻辑
func (r *Rescheduler) performRescheduling(ctx context.Context) error {
	r.logger.V(2).Info("开始执行重调度检查")

	// 获取所有节点和Pod
	nodes, err := r.nodeLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("获取节点列表失败: %v", err)
	}

	pods, err := r.podLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("获取Pod列表失败: %v", err)
	}

	// 计算节点资源使用情况
	nodeUsages := r.calculateNodeUsages(nodes, pods)

	var allDecisions []ReschedulingDecision

	// 执行启用的重调度策略
	for _, strategy := range r.config.EnabledStrategies {
		switch strategy {
		case LoadBalancingStrategy:
			decisions := r.performLoadBalancing(nodeUsages, pods)
			allDecisions = append(allDecisions, decisions...)
		case ResourceOptimizationStrategy:
			decisions := r.performResourceOptimization(nodeUsages, pods)
			allDecisions = append(allDecisions, decisions...)
		case NodeMaintenanceStrategy:
			decisions := r.performNodeMaintenance(nodeUsages, pods)
			allDecisions = append(allDecisions, decisions...)
		}
	}

	// 调试：输出决策数量
	r.logger.Info("重调度决策生成", "决策数量", len(allDecisions), "最大重调度数量", r.config.MaxReschedulePods)

	// 限制重调度数量
	if len(allDecisions) > r.config.MaxReschedulePods {
		allDecisions = allDecisions[:r.config.MaxReschedulePods]
	}

	// 执行重调度决策 - 使用协调机制避免冲突
	for _, decision := range allDecisions {
		// 优先使用Deployment协调器，回退到原有控制器
		r.logger.Info("检查协调器状态", "deploymentCoordinator", r.deploymentCoordinator != nil)
		if r.deploymentCoordinator != nil {
			r.logger.Info("启用协调重调度", "pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name))
			err := r.deploymentCoordinator.CoordinatedRescheduling(ctx, decision)
			if err != nil {
				r.logger.Error(err, "协调重调度失败，回退到原有机制",
					"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name))
				// 回退到原有机制
				if err := r.controller.ExecuteMigration(ctx, decision); err != nil {
					r.logger.Error(err, "提交Pod迁移任务失败",
						"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
						"sourceNode", decision.SourceNode,
						"targetNode", decision.TargetNode)
				}
			}
		} else {
			// 没有协调器时使用原有机制
			if err := r.controller.ExecuteMigration(ctx, decision); err != nil {
				r.logger.Error(err, "提交Pod迁移任务失败",
					"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
					"sourceNode", decision.SourceNode,
					"targetNode", decision.TargetNode)
			}
		}
	}

	if len(allDecisions) > 0 {
		r.logger.Info("完成重调度操作", "重调度Pod数量", len(allDecisions))
	}

	return nil
}

// calculateNodeUsages 计算节点资源使用情况
func (r *Rescheduler) calculateNodeUsages(nodes []*v1.Node, pods []*v1.Pod) map[string]*NodeResourceUsage {
	nodeUsages := make(map[string]*NodeResourceUsage)

	// 初始化节点使用情况
	for _, node := range nodes {
		if node.Spec.Unschedulable {
			continue // 跳过不可调度的节点
		}

		nodeUsages[node.Name] = &NodeResourceUsage{
			Node:     node,
			PodCount: 0,
		}
	}

	// 计算每个节点上的资源使用情况
	for _, pod := range pods {
		if pod.Status.Phase != v1.PodRunning {
			continue
		}

		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}

		usage, exists := nodeUsages[nodeName]
		if !exists {
			continue
		}

		usage.PodCount++

		// 计算资源请求
		for _, container := range pod.Spec.Containers {
			if cpu := container.Resources.Requests.Cpu(); cpu != nil {
				// 累加CPU请求 (转换为毫核)
				usage.CPURequests += cpu.MilliValue()
			}
			if memory := container.Resources.Requests.Memory(); memory != nil {
				// 累加内存请求 (单位为字节)
				usage.MemoryRequests += memory.Value()
			}
		}
	}

	// 计算资源使用百分比
	for _, usage := range nodeUsages {
		node := usage.Node

		// 获取节点容量
		cpuCapacity := node.Status.Capacity.Cpu()
		memCapacity := node.Status.Capacity.Memory()

		if cpuCapacity != nil && memCapacity != nil {
			// 计算实际的资源使用率
			cpuCapacityMilliValue := cpuCapacity.MilliValue()
			memCapacityValue := memCapacity.Value()

			if cpuCapacityMilliValue > 0 {
				usage.CPUUsagePercent = float64(usage.CPURequests) / float64(cpuCapacityMilliValue) * 100.0
			}

			if memCapacityValue > 0 {
				usage.MemoryUsagePercent = float64(usage.MemoryRequests) / float64(memCapacityValue) * 100.0
			}

			// 计算综合分数 (使用率越低分数越高)
			usage.Score = 200.0 - usage.CPUUsagePercent - usage.MemoryUsagePercent
		}
	}

	return nodeUsages
}

// performLoadBalancing 执行负载均衡重调度
func (r *Rescheduler) performLoadBalancing(nodeUsages map[string]*NodeResourceUsage, pods []*v1.Pod) []ReschedulingDecision {
	var decisions []ReschedulingDecision

	// 按使用率排序节点
	var sortedNodes []*NodeResourceUsage
	for _, usage := range nodeUsages {
		sortedNodes = append(sortedNodes, usage)
	}

	sort.Slice(sortedNodes, func(i, j int) bool {
		return sortedNodes[i].CPUUsagePercent > sortedNodes[j].CPUUsagePercent
	})

	if len(sortedNodes) < 2 {
		return decisions
	}

	// 检查是否需要负载均衡
	highestUsage := sortedNodes[0].CPUUsagePercent
	lowestUsage := sortedNodes[len(sortedNodes)-1].CPUUsagePercent

	if highestUsage-lowestUsage > r.config.ImbalanceThreshold {
		// 从高负载节点迁移Pod到低负载节点
		sourceNode := sortedNodes[0]
		targetNode := sortedNodes[len(sortedNodes)-1]

		// 查找可迁移的Pod
		candidatePods := r.findMigratablePods(pods, sourceNode.Node.Name)

		if len(candidatePods) > 0 {
			decisions = append(decisions, ReschedulingDecision{
				Pod:        candidatePods[0],
				SourceNode: sourceNode.Node.Name,
				TargetNode: targetNode.Node.Name,
				Reason:     fmt.Sprintf("负载均衡: 源节点使用率%.1f%%, 目标节点使用率%.1f%%", highestUsage, lowestUsage),
				Strategy:   LoadBalancingStrategy,
			})
		}
	}

	return decisions
}

// performResourceOptimization 执行资源优化重调度
func (r *Rescheduler) performResourceOptimization(nodeUsages map[string]*NodeResourceUsage, pods []*v1.Pod) []ReschedulingDecision {
	var decisions []ReschedulingDecision

	// 查找高负载节点
	for _, usage := range nodeUsages {
		if usage.CPUUsagePercent > r.config.CPUThreshold || usage.MemoryUsagePercent > r.config.MemoryThreshold {
			// 查找可迁移的Pod
			candidatePods := r.findMigratablePods(pods, usage.Node.Name)

			// 查找最佳目标节点
			targetNode := r.findBestTargetNode(nodeUsages, usage.Node.Name)

			if len(candidatePods) > 0 && targetNode != "" {
				decisions = append(decisions, ReschedulingDecision{
					Pod:        candidatePods[0],
					SourceNode: usage.Node.Name,
					TargetNode: targetNode,
					Reason:     fmt.Sprintf("资源优化: 节点CPU使用率%.1f%%, 内存使用率%.1f%%", usage.CPUUsagePercent, usage.MemoryUsagePercent),
					Strategy:   ResourceOptimizationStrategy,
				})

				if len(decisions) >= r.config.MaxReschedulePods {
					break
				}
			}
		}
	}

	return decisions
}

// performNodeMaintenance 执行节点维护重调度
func (r *Rescheduler) performNodeMaintenance(nodeUsages map[string]*NodeResourceUsage, pods []*v1.Pod) []ReschedulingDecision {
	var decisions []ReschedulingDecision

	// 检查是否有节点需要维护 (通过注解或标签标识)
	for _, usage := range nodeUsages {
		node := usage.Node

		// 检查维护标签
		if value, exists := node.Labels["scheduler.alpha.kubernetes.io/maintenance"]; exists && value == "true" {
			// 迁移该节点上的所有Pod
			candidatePods := r.findMigratablePods(pods, node.Name)

			for _, pod := range candidatePods {
				targetNode := r.findBestTargetNode(nodeUsages, node.Name)
				if targetNode != "" {
					decisions = append(decisions, ReschedulingDecision{
						Pod:        pod,
						SourceNode: node.Name,
						TargetNode: targetNode,
						Reason:     "节点维护模式",
						Strategy:   NodeMaintenanceStrategy,
					})
				}

				if len(decisions) >= r.config.MaxReschedulePods {
					break
				}
			}
		}
	}

	return decisions
}

// findMigratablePods 查找可迁移的Pod
func (r *Rescheduler) findMigratablePods(pods []*v1.Pod, nodeName string) []*v1.Pod {
	var candidatePods []*v1.Pod

	for _, pod := range pods {
		if pod.Spec.NodeName != nodeName || pod.Status.Phase != v1.PodRunning {
			continue
		}

		// 排除系统命名空间
		if r.isExcludedNamespace(pod.Namespace) {
			continue
		}

		// 排除静态Pod
		if pod.Annotations["kubernetes.io/config.source"] == "api" {
			continue
		}

		// 排除DaemonSet Pod
		for _, ownerRef := range pod.OwnerReferences {
			if ownerRef.Kind == "DaemonSet" {
				continue
			}
		}

		// 检查Pod是否有特殊的重调度排除标签
		if value, exists := pod.Labels["scheduler.alpha.kubernetes.io/rescheduling"]; exists && value == "disabled" {
			continue
		}

		candidatePods = append(candidatePods, pod)
	}

	// 按优先级排序 (优先级低的先迁移)
	sort.Slice(candidatePods, func(i, j int) bool {
		pi := int32(0)
		pj := int32(0)
		if candidatePods[i].Spec.Priority != nil {
			pi = *candidatePods[i].Spec.Priority
		}
		if candidatePods[j].Spec.Priority != nil {
			pj = *candidatePods[j].Spec.Priority
		}
		return pi < pj
	})

	return candidatePods
}

// findBestTargetNode 查找最佳目标节点
func (r *Rescheduler) findBestTargetNode(nodeUsages map[string]*NodeResourceUsage, excludeNode string) string {
	var bestNode string
	bestScore := -math.MaxFloat64

	for nodeName, usage := range nodeUsages {
		if nodeName == excludeNode {
			continue
		}

		// 检查节点是否可用
		if usage.Node.Spec.Unschedulable {
			continue
		}

		// 检查资源使用情况
		if usage.CPUUsagePercent > r.config.CPUThreshold || usage.MemoryUsagePercent > r.config.MemoryThreshold {
			continue
		}

		// 选择分数最高的节点
		if usage.Score > bestScore {
			bestScore = usage.Score
			bestNode = nodeName
		}
	}

	return bestNode
}

// isExcludedNamespace 检查命名空间是否被排除
func (r *Rescheduler) isExcludedNamespace(namespace string) bool {
	for _, excluded := range r.config.ExcludedNamespaces {
		if namespace == excluded {
			return true
		}
	}
	return false
}

// Filter 实现FilterPlugin接口 - 在调度新Pod时过滤过载节点
func (r *Rescheduler) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	if !r.config.EnableSchedulingOptimization {
		return nil // 如果未启用调度优化，直接通过
	}

	// 获取当前节点资源使用情况
	nodeUsage := r.getNodeUsage(nodeInfo.Node())

	// 检查节点是否过载
	if nodeUsage.CPUUsagePercent > r.config.CPUThreshold {
		return framework.NewStatus(framework.Unschedulable,
			fmt.Sprintf("节点CPU使用率%.1f%%超过阈值%.1f%%",
				nodeUsage.CPUUsagePercent, r.config.CPUThreshold))
	}

	if nodeUsage.MemoryUsagePercent > r.config.MemoryThreshold {
		return framework.NewStatus(framework.Unschedulable,
			fmt.Sprintf("节点内存使用率%.1f%%超过阈值%.1f%%",
				nodeUsage.MemoryUsagePercent, r.config.MemoryThreshold))
	}

	// 检查是否为维护模式节点
	if value, exists := nodeInfo.Node().Labels["scheduler.alpha.kubernetes.io/maintenance"]; exists && value == "true" {
		return framework.NewStatus(framework.Unschedulable, "节点处于维护模式")
	}

	r.logger.V(4).Info("节点通过过滤检查",
		"node", nodeInfo.Node().Name,
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"cpuUsage", nodeUsage.CPUUsagePercent,
		"memoryUsage", nodeUsage.MemoryUsagePercent)

	return nil
}

// Score 实现ScorePlugin接口 - 为节点打分，偏好低负载节点
func (r *Rescheduler) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) (int64, *framework.Status) {
	if !r.config.EnableSchedulingOptimization {
		return 0, nil // 如果未启用调度优化，返回默认分数
	}

	node := nodeInfo.Node()
	nodeUsage := r.getNodeUsage(node)

	// 计算负载均衡分数 (使用率越低分数越高)
	// 分数范围: 0-100
	cpuScore := (100.0 - nodeUsage.CPUUsagePercent) * r.config.CPUScoreWeight
	memoryScore := (100.0 - nodeUsage.MemoryUsagePercent) * r.config.MemoryScoreWeight

	totalScore := cpuScore + memoryScore

	// 特殊情况加分
	if nodeUsage.CPUUsagePercent < 30 && nodeUsage.MemoryUsagePercent < 30 {
		totalScore += r.config.LoadBalanceBonus // 低负载节点额外加分
	}

	// 避免重调度目标节点 - 减少不必要的迁移
	if r.isRecentReschedulingTarget(node.Name) {
		totalScore += 5 // 最近作为迁移目标的节点加分
	}

	score := int64(math.Max(0, math.Min(100, totalScore)))

	r.logger.V(4).Info("节点打分结果",
		"node", node.Name,
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"cpuUsage", nodeUsage.CPUUsagePercent,
		"memoryUsage", nodeUsage.MemoryUsagePercent,
		"score", score)

	return score, nil
}

// ScoreExtensions 返回空，使用默认的归一化
func (r *Rescheduler) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// PreBind 增强实现 - 在绑定新Pod前检查是否需要预防性重调度
func (r *Rescheduler) PreBind(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	if !r.config.EnablePreventiveRescheduling {
		return nil // 如果未启用预防性重调度，直接通过
	}

	// 获取目标节点的当前状态
	node, err := r.nodeLister.Get(nodeName)
	if err != nil {
		return framework.AsStatus(fmt.Errorf("获取节点信息失败: %v", err))
	}

	// 预测Pod调度后的节点使用情况
	predictedUsage := r.predictNodeUsageAfterPodScheduling(node, pod)

	r.logger.Info("Pod调度预测分析",
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"targetNode", nodeName,
		"predictedCPU", predictedUsage.CPUUsagePercent,
		"predictedMemory", predictedUsage.MemoryUsagePercent)

	// 如果预测调度后节点仍然健康，直接通过
	if predictedUsage.CPUUsagePercent <= r.config.CPUThreshold &&
		predictedUsage.MemoryUsagePercent <= r.config.MemoryThreshold {
		return nil
	}

	// 如果预测调度后可能过载，触发预防性重调度
	r.logger.Info("触发预防性重调度",
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"targetNode", nodeName,
		"reason", "预防节点过载")

	// 异步执行预防性重调度，不阻塞当前Pod调度
	go r.performPreventiveRescheduling(ctx, nodeName, predictedUsage)

	return nil
}

// getNodeUsage 获取节点实时使用情况
func (r *Rescheduler) getNodeUsage(node *v1.Node) *NodeResourceUsage {
	// 获取节点上的所有Pod
	pods, err := r.podLister.List(labels.Everything())
	if err != nil {
		r.logger.Error(err, "获取Pod列表失败")
		return &NodeResourceUsage{Node: node}
	}

	// 计算节点资源使用情况
	nodeUsages := r.calculateNodeUsages([]*v1.Node{node}, pods)
	if usage, exists := nodeUsages[node.Name]; exists {
		return usage
	}

	return &NodeResourceUsage{Node: node}
}

// predictNodeUsageAfterPodScheduling 预测Pod调度后的节点使用情况
func (r *Rescheduler) predictNodeUsageAfterPodScheduling(node *v1.Node, newPod *v1.Pod) *NodeResourceUsage {
	currentUsage := r.getNodeUsage(node)

	// 计算新Pod的资源需求
	var additionalCPU, additionalMemory int64
	for _, container := range newPod.Spec.Containers {
		if cpu := container.Resources.Requests.Cpu(); cpu != nil {
			additionalCPU += cpu.MilliValue()
		}
		if memory := container.Resources.Requests.Memory(); memory != nil {
			additionalMemory += memory.Value()
		}
	}

	// 计算预测使用率
	nodeCapacityCPU := node.Status.Capacity.Cpu().MilliValue()
	nodeCapacityMemory := node.Status.Capacity.Memory().Value()

	predictedCPUUsage := currentUsage.CPUUsagePercent
	predictedMemoryUsage := currentUsage.MemoryUsagePercent

	if nodeCapacityCPU > 0 {
		predictedCPUUsage += float64(additionalCPU) / float64(nodeCapacityCPU) * 100
	}
	if nodeCapacityMemory > 0 {
		predictedMemoryUsage += float64(additionalMemory) / float64(nodeCapacityMemory) * 100
	}

	return &NodeResourceUsage{
		Node:               node,
		CPUUsagePercent:    predictedCPUUsage,
		MemoryUsagePercent: predictedMemoryUsage,
		CPURequests:        currentUsage.CPURequests + additionalCPU,
		MemoryRequests:     currentUsage.MemoryRequests + additionalMemory,
		PodCount:           currentUsage.PodCount + 1,
	}
}

// performPreventiveRescheduling 执行预防性重调度
func (r *Rescheduler) performPreventiveRescheduling(ctx context.Context, nodeName string, predictedUsage *NodeResourceUsage) {
	r.logger.Info("开始执行预防性重调度",
		"targetNode", nodeName,
		"predictedCPU", predictedUsage.CPUUsagePercent,
		"predictedMemory", predictedUsage.MemoryUsagePercent)

	// 获取节点上可迁移的Pod
	pods, err := r.podLister.List(labels.Everything())
	if err != nil {
		r.logger.Error(err, "获取Pod列表失败")
		return
	}

	candidatePods := r.findMigratablePods(pods, nodeName)
	if len(candidatePods) == 0 {
		r.logger.Info("没有找到可迁移的Pod", "node", nodeName)
		return
	}

	// 根据预测使用率决定需要迁移的Pod数量
	// 使用率越高，迁移的Pod越多（最多3个）
	var podsToMigrate int
	avgPredictedUsage := (predictedUsage.CPUUsagePercent + predictedUsage.MemoryUsagePercent) / 2
	if avgPredictedUsage > 90 {
		podsToMigrate = 3
	} else if avgPredictedUsage > 80 {
		podsToMigrate = 2
	} else {
		podsToMigrate = 1
	}

	// 限制迁移数量不超过可用Pod数量
	if podsToMigrate > len(candidatePods) {
		podsToMigrate = len(candidatePods)
	}

	r.logger.Info("确定迁移策略",
		"node", nodeName,
		"avgPredictedUsage", avgPredictedUsage,
		"podsToMigrate", podsToMigrate,
		"availablePods", len(candidatePods))

	// 计算节点使用情况（用于找目标节点）
	nodes, _ := r.nodeLister.List(labels.Everything())
	nodeUsages := r.calculateNodeUsages(nodes, pods)

	// 为每个需要迁移的Pod执行迁移
	for i := 0; i < podsToMigrate; i++ {
		migrationPod := candidatePods[i]

		targetNode := r.findBestTargetNode(nodeUsages, nodeName)
		if targetNode == "" {
			r.logger.Info("没有找到合适的目标节点", "sourceNode", nodeName, "podIndex", i)
			continue
		}

		// 记录作为迁移目标
		r.markAsReschedulingTarget(targetNode)

		// 创建预防性重调度决策，包含预测使用率信息
		reason := fmt.Sprintf("预防性重调度：避免节点过载，预测CPU使用率%.1f%%，内存使用率%.1f%%",
			predictedUsage.CPUUsagePercent, predictedUsage.MemoryUsagePercent)

		decision := ReschedulingDecision{
			Pod:        migrationPod,
			SourceNode: nodeName,
			TargetNode: targetNode,
			Reason:     reason,
			Strategy:   "PreventiveLoadBalancing",
		}

		// 执行迁移
		var migrationErr error
		if r.deploymentCoordinator != nil {
			migrationErr = r.deploymentCoordinator.CoordinatedRescheduling(ctx, decision)
		} else {
			migrationErr = r.controller.ExecuteMigration(ctx, decision)
		}

		if migrationErr != nil {
			r.logger.Error(migrationErr, "预防性重调度失败",
				"decision", decision,
				"podIndex", i)
		} else {
			r.logger.Info("预防性重调度成功",
				"decision", decision,
				"podIndex", i)
		}
	}

	r.logger.Info("预防性重调度完成",
		"node", nodeName,
		"totalPodsAttempted", podsToMigrate)
}

// isRecentReschedulingTarget 检查节点是否为最近的重调度目标（并发安全）
func (r *Rescheduler) isRecentReschedulingTarget(nodeName string) bool {
	r.targetsMutex.RLock()
	defer r.targetsMutex.RUnlock()

	if lastTime, exists := r.recentReschedulingTargets[nodeName]; exists {
		// 如果5分钟内作为过迁移目标，给予加分
		return time.Since(lastTime) < 5*time.Minute
	}
	return false
}

// markAsReschedulingTarget 标记节点为迁移目标（并发安全）
func (r *Rescheduler) markAsReschedulingTarget(nodeName string) {
	r.targetsMutex.Lock()
	defer r.targetsMutex.Unlock()

	r.recentReschedulingTargets[nodeName] = time.Now()

	// 清理过期记录（保持map大小合理）
	cutoffTime := time.Now().Add(-10 * time.Minute)
	for node, timestamp := range r.recentReschedulingTargets {
		if timestamp.Before(cutoffTime) {
			delete(r.recentReschedulingTargets, node)
		}
	}
}

// ReschedulerArgs 插件配置参数结构体（支持JSON和YAML）
type ReschedulerArgs struct {
	ReschedulingInterval         string   `json:"reschedulingInterval,omitempty" yaml:"reschedulingInterval,omitempty"`
	EnabledStrategies            []string `json:"enabledStrategies,omitempty" yaml:"enabledStrategies,omitempty"`
	CPUThreshold                 float64  `json:"cpuThreshold,omitempty" yaml:"cpuThreshold,omitempty"`
	MemoryThreshold              float64  `json:"memoryThreshold,omitempty" yaml:"memoryThreshold,omitempty"`
	ImbalanceThreshold           float64  `json:"imbalanceThreshold,omitempty" yaml:"imbalanceThreshold,omitempty"`
	MaxReschedulePods            int      `json:"maxReschedulePods,omitempty" yaml:"maxReschedulePods,omitempty"`
	ExcludedNamespaces           []string `json:"excludedNamespaces,omitempty" yaml:"excludedNamespaces,omitempty"`
	ExcludedPodSelector          string   `json:"excludedPodSelector,omitempty" yaml:"excludedPodSelector,omitempty"`
	EnableSchedulingOptimization *bool    `json:"enableSchedulingOptimization,omitempty" yaml:"enableSchedulingOptimization,omitempty"` // 指针类型支持三态
	EnablePreventiveRescheduling *bool    `json:"enablePreventiveRescheduling,omitempty" yaml:"enablePreventiveRescheduling,omitempty"` // 指针类型支持三态
	CPUScoreWeight               float64  `json:"cpuScoreWeight,omitempty" yaml:"cpuScoreWeight,omitempty"`
	MemoryScoreWeight            float64  `json:"memoryScoreWeight,omitempty" yaml:"memoryScoreWeight,omitempty"`
	LoadBalanceBonus             float64  `json:"loadBalanceBonus,omitempty" yaml:"loadBalanceBonus,omitempty"`
}

// parseConfigData 智能解析配置数据，支持JSON和YAML格式
func parseConfigData(rawData []byte, pluginArgs *ReschedulerArgs, logger klog.Logger) error {
	// 先尝试JSON解析（Kubernetes内部通常使用JSON）
	if err := json.Unmarshal(rawData, pluginArgs); err == nil {
		logger.V(2).Info("使用JSON格式成功解析配置")
		return nil
	} else {
		logger.V(3).Info("JSON解析失败，尝试YAML解析", "jsonError", err.Error())
	}

	// JSON解析失败，尝试YAML解析
	if err := yaml.Unmarshal(rawData, pluginArgs); err == nil {
		logger.V(2).Info("使用YAML格式成功解析配置")
		return nil
	} else {
		logger.V(3).Info("YAML解析也失败", "yamlError", err.Error())
	}

	// 尝试YAML转JSON再解析（处理一些特殊的YAML格式）
	jsonData, err := yaml.ToJSON(rawData)
	if err != nil {
		return fmt.Errorf("YAML转JSON失败: %v", err)
	}

	if err := json.Unmarshal(jsonData, pluginArgs); err == nil {
		logger.V(2).Info("通过YAML→JSON转换成功解析配置")
		return nil
	}

	return fmt.Errorf("配置解析失败，尝试了JSON、YAML和YAML→JSON三种方式")
}

// parsePluginConfig 解析插件配置
func parsePluginConfig(obj runtime.Object, config *ReschedulerConfig, logger klog.Logger) error {
	if obj == nil {
		logger.V(2).Info("配置对象为空，使用默认配置")
		return nil
	}

	var pluginArgs ReschedulerArgs
	var err error

	// 处理不同类型的配置对象
	switch v := obj.(type) {
	case *runtime.Unknown:
		if v != nil && len(v.Raw) > 0 {
			logger.V(2).Info("解析runtime.Unknown配置", "contentType", v.ContentType, "rawSize", len(v.Raw))

			// 智能解析：先尝试JSON，再尝试YAML
			if err = parseConfigData(v.Raw, &pluginArgs, logger); err != nil {
				logger.Error(err, "配置解析失败",
					"rawConfig", string(v.Raw),
					"contentType", v.ContentType)
				return fmt.Errorf("解析插件配置失败: %v", err)
			}
		} else {
			logger.V(2).Info("runtime.Unknown配置为空，使用默认配置")
			return nil
		}

	default:
		// 对于其他类型，尝试JSON序列化后再反序列化
		logger.V(2).Info("处理配置对象，尝试JSON转换", "type", fmt.Sprintf("%T", obj))

		jsonData, err := json.Marshal(obj)
		if err != nil {
			logger.Error(err, "序列化配置对象失败", "type", fmt.Sprintf("%T", obj))
			return fmt.Errorf("序列化配置对象失败: %v", err)
		}

		if err = json.Unmarshal(jsonData, &pluginArgs); err != nil {
			logger.Error(err, "反序列化配置失败", "jsonData", string(jsonData))
			return fmt.Errorf("反序列化配置失败: %v", err)
		}

		logger.V(2).Info("通过JSON转换成功解析配置", "jsonData", string(jsonData))
	}

	// 应用解析后的配置
	if err := applyPluginConfig(&pluginArgs, config, logger); err != nil {
		return fmt.Errorf("应用配置失败: %v", err)
	}

	logger.Info("插件配置解析和应用成功", "配置摘要", formatConfigSummary(config))
	return nil
}

// applyPluginConfig 将解析后的配置应用到ReschedulerConfig
func applyPluginConfig(args *ReschedulerArgs, config *ReschedulerConfig, logger klog.Logger) error {
	// 解析重调度间隔
	if args.ReschedulingInterval != "" {
		interval, err := time.ParseDuration(args.ReschedulingInterval)
		if err != nil {
			return fmt.Errorf("解析重调度间隔失败: %v", err)
		}
		config.ReschedulingInterval = interval
		logger.V(2).Info("设置重调度间隔", "interval", interval)
	}

	// 启用的策略
	if len(args.EnabledStrategies) > 0 {
		config.EnabledStrategies = args.EnabledStrategies
		logger.V(2).Info("设置启用策略", "strategies", args.EnabledStrategies)
	}

	// CPU阈值
	if args.CPUThreshold > 0 {
		if args.CPUThreshold > 100 {
			return fmt.Errorf("CPU阈值不能超过100%%: %v", args.CPUThreshold)
		}
		config.CPUThreshold = args.CPUThreshold
		logger.V(2).Info("设置CPU阈值", "threshold", args.CPUThreshold)
	}

	// 内存阈值
	if args.MemoryThreshold > 0 {
		if args.MemoryThreshold > 100 {
			return fmt.Errorf("内存阈值不能超过100%%: %v", args.MemoryThreshold)
		}
		config.MemoryThreshold = args.MemoryThreshold
		logger.V(2).Info("设置内存阈值", "threshold", args.MemoryThreshold)
	}

	// 负载不均衡阈值
	if args.ImbalanceThreshold > 0 {
		if args.ImbalanceThreshold > 100 {
			return fmt.Errorf("负载不均衡阈值不能超过100%%: %v", args.ImbalanceThreshold)
		}
		config.ImbalanceThreshold = args.ImbalanceThreshold
		logger.V(2).Info("设置负载不均衡阈值", "threshold", args.ImbalanceThreshold)
	}

	// 最大重调度Pod数量
	if args.MaxReschedulePods > 0 {
		config.MaxReschedulePods = args.MaxReschedulePods
		logger.V(2).Info("设置最大重调度Pod数量", "max", args.MaxReschedulePods)
	}

	// 排除的命名空间
	if len(args.ExcludedNamespaces) > 0 {
		config.ExcludedNamespaces = args.ExcludedNamespaces
		logger.V(2).Info("设置排除命名空间", "namespaces", args.ExcludedNamespaces)
	}

	// 排除的Pod标签选择器
	if args.ExcludedPodSelector != "" {
		config.ExcludedPodSelector = args.ExcludedPodSelector
		logger.V(2).Info("设置排除Pod标签选择器", "selector", args.ExcludedPodSelector)
	}

	// 调度优化开关（正确处理bool指针值）
	if args.EnableSchedulingOptimization != nil {
		config.EnableSchedulingOptimization = *args.EnableSchedulingOptimization
		logger.V(2).Info("设置调度优化开关", "enabled", *args.EnableSchedulingOptimization)
	}

	// 预防性重调度开关
	if args.EnablePreventiveRescheduling != nil {
		config.EnablePreventiveRescheduling = *args.EnablePreventiveRescheduling
		logger.V(2).Info("设置预防性重调度开关", "enabled", *args.EnablePreventiveRescheduling)
	}

	// CPU权重
	if args.CPUScoreWeight > 0 {
		if args.CPUScoreWeight > 1.0 {
			return fmt.Errorf("CPU权重不能超过1.0: %v", args.CPUScoreWeight)
		}
		config.CPUScoreWeight = args.CPUScoreWeight
		logger.V(2).Info("设置CPU权重", "weight", args.CPUScoreWeight)
	}

	// 内存权重
	if args.MemoryScoreWeight > 0 {
		if args.MemoryScoreWeight > 1.0 {
			return fmt.Errorf("内存权重不能超过1.0: %v", args.MemoryScoreWeight)
		}
		config.MemoryScoreWeight = args.MemoryScoreWeight
		logger.V(2).Info("设置内存权重", "weight", args.MemoryScoreWeight)
	}

	// 负载均衡奖励
	if args.LoadBalanceBonus > 0 {
		config.LoadBalanceBonus = args.LoadBalanceBonus
		logger.V(2).Info("设置负载均衡奖励", "bonus", args.LoadBalanceBonus)
	}

	// 验证配置的完整性和合理性
	return validateConfig(config, logger)
}

// validateConfig 验证配置的合理性
func validateConfig(config *ReschedulerConfig, logger klog.Logger) error {
	// 验证重调度间隔
	if config.ReschedulingInterval < time.Second {
		return fmt.Errorf("重调度间隔过小，最小值为1秒，当前值: %v", config.ReschedulingInterval)
	}
	if config.ReschedulingInterval > 10*time.Minute {
		logger.Error(nil, "警告：重调度间隔过长，可能影响响应性", "interval", config.ReschedulingInterval)
	}

	// 验证策略
	validStrategies := map[string]bool{
		LoadBalancingStrategy:        true,
		ResourceOptimizationStrategy: true,
		NodeMaintenanceStrategy:      true,
	}
	for _, strategy := range config.EnabledStrategies {
		if !validStrategies[strategy] {
			return fmt.Errorf("无效的重调度策略: %s", strategy)
		}
	}

	// 验证阈值范围
	if config.CPUThreshold <= 0 || config.CPUThreshold > 100 {
		return fmt.Errorf("CPU阈值必须在0-100之间: %v", config.CPUThreshold)
	}
	if config.MemoryThreshold <= 0 || config.MemoryThreshold > 100 {
		return fmt.Errorf("内存阈值必须在0-100之间: %v", config.MemoryThreshold)
	}
	if config.ImbalanceThreshold <= 0 || config.ImbalanceThreshold > 100 {
		return fmt.Errorf("负载不均衡阈值必须在0-100之间: %v", config.ImbalanceThreshold)
	}

	// 验证MaxReschedulePods
	if config.MaxReschedulePods <= 0 {
		return fmt.Errorf("最大重调度Pod数量必须大于0: %v", config.MaxReschedulePods)
	}
	if config.MaxReschedulePods > 100 {
		logger.Error(nil, "警告：最大重调度Pod数量过大，可能影响集群稳定性", "max", config.MaxReschedulePods)
	}

	// 验证权重
	if config.CPUScoreWeight < 0 || config.CPUScoreWeight > 1.0 {
		return fmt.Errorf("CPU权重必须在0-1之间: %v", config.CPUScoreWeight)
	}
	if config.MemoryScoreWeight < 0 || config.MemoryScoreWeight > 1.0 {
		return fmt.Errorf("内存权重必须在0-1之间: %v", config.MemoryScoreWeight)
	}

	// 验证权重总和
	weightSum := config.CPUScoreWeight + config.MemoryScoreWeight
	if weightSum > 1.0 {
		logger.Error(nil, "警告：CPU和内存权重之和超过1.0，可能导致打分异常",
			"cpuWeight", config.CPUScoreWeight,
			"memoryWeight", config.MemoryScoreWeight,
			"总和", weightSum)
	}
	if weightSum < 0.5 {
		logger.Error(nil, "警告：CPU和内存权重之和过小，可能导致打分不敏感",
			"cpuWeight", config.CPUScoreWeight,
			"memoryWeight", config.MemoryScoreWeight,
			"总和", weightSum)
	}

	// 验证负载均衡奖励
	if config.LoadBalanceBonus < 0 || config.LoadBalanceBonus > 50 {
		logger.Error(nil, "警告：负载均衡奖励值异常，建议范围0-50", "bonus", config.LoadBalanceBonus)
	}

	// 验证命名空间
	for _, ns := range config.ExcludedNamespaces {
		if ns == "" {
			return fmt.Errorf("排除命名空间列表包含空字符串")
		}
	}

	// 逻辑一致性检查
	if config.EnablePreventiveRescheduling && !config.EnableSchedulingOptimization {
		logger.Error(nil, "警告：启用预防性重调度但未启用调度优化，预防性重调度可能无效")
	}

	logger.V(2).Info("配置验证通过")
	return nil
}

// formatConfigSummary 格式化配置摘要用于日志
func formatConfigSummary(config *ReschedulerConfig) string {
	return fmt.Sprintf("interval=%v, strategies=%v, cpuThreshold=%.1f%%, memoryThreshold=%.1f%%, schedulingOpt=%v, preventive=%v",
		config.ReschedulingInterval,
		config.EnabledStrategies,
		config.CPUThreshold,
		config.MemoryThreshold,
		config.EnableSchedulingOptimization,
		config.EnablePreventiveRescheduling)
}

// Stop 停止重调度器
func (r *Rescheduler) Stop() {
	if r.controller != nil {
		r.controller.Stop()
	}
	close(r.stopCh)
}
