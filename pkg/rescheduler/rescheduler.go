package rescheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
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
		ReschedulingInterval: DefaultReschedulingInterval,
		EnabledStrategies:    []string{LoadBalancingStrategy, ResourceOptimizationStrategy},
		CPUThreshold:         DefaultCPUThreshold,
		MemoryThreshold:      DefaultMemoryThreshold,
		ImbalanceThreshold:   DefaultImbalanceThreshold,
		MaxReschedulePods:    10,
		ExcludedNamespaces:   []string{"kube-system", "kube-public"},
	}

	// TODO: 从obj中解析实际配置

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
		logger:                logger,
		handle:                h,
		config:                config,
		clientset:             h.ClientSet(),
		podLister:             h.SharedInformerFactory().Core().V1().Pods().Lister(),
		nodeLister:            h.SharedInformerFactory().Core().V1().Nodes().Lister(),
		controller:            controller,
		deploymentCoordinator: deploymentCoordinator,
		stopCh:                make(chan struct{}),
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

// executeMigration 已弃用：现在通过控制器执行Pod迁移
// 保留此方法以兼容现有接口，但实际迁移逻辑已移至ReschedulerController
/*func (r *Rescheduler) executeMigration(ctx context.Context, decision ReschedulingDecision) error {
	r.logger.Info("重定向到控制器执行Pod迁移",
		"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
		"sourceNode", decision.SourceNode,
		"targetNode", decision.TargetNode,
		"reason", decision.Reason,
		"strategy", decision.Strategy)

	// 通过控制器执行迁移
	return r.controller.ExecuteMigration(ctx, decision)
}
*/
// PreBind 实现PreBindPlugin接口，这样插件能被调度器加载
// 我们不在这里做任何调度相关的操作，只是为了让插件能被初始化
func (r *Rescheduler) PreBind(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	return nil // 不做任何操作，直接返回成功
}

// Stop 停止重调度器
func (r *Rescheduler) Stop() {
	if r.controller != nil {
		r.controller.Stop()
	}
	close(r.stopCh)
}
