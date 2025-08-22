/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rescheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	DefaultCPUThreshold         = 80.0
	DefaultMemoryThreshold      = 80.0
	DefaultImbalanceThreshold   = 20.0
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
	PodCount           int
	Score              float64
}

// 确保Rescheduler实现了相关接口
var _ framework.Plugin = &Rescheduler{}

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

	rescheduler := &Rescheduler{
		logger:     logger,
		handle:     h,
		config:     config,
		clientset:  h.ClientSet(),
		podLister:  h.SharedInformerFactory().Core().V1().Pods().Lister(),
		nodeLister: h.SharedInformerFactory().Core().V1().Nodes().Lister(),
		stopCh:     make(chan struct{}),
	}

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

	// 限制重调度数量
	if len(allDecisions) > r.config.MaxReschedulePods {
		allDecisions = allDecisions[:r.config.MaxReschedulePods]
	}

	// 执行重调度决策
	for _, decision := range allDecisions {
		if err := r.executeMigration(ctx, decision); err != nil {
			r.logger.Error(err, "执行Pod迁移失败",
				"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
				"sourceNode", decision.SourceNode,
				"targetNode", decision.TargetNode)
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
				// TODO: 累加CPU请求
			}
			if memory := container.Resources.Requests.Memory(); memory != nil {
				// TODO: 累加内存请求
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
			// TODO: 实现精确的资源使用率计算
			// 这里使用简化的计算方式
			usage.CPUUsagePercent = float64(usage.PodCount) * 10.0   // 简化计算
			usage.MemoryUsagePercent = float64(usage.PodCount) * 8.0 // 简化计算

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

// executeMigration 执行Pod迁移
func (r *Rescheduler) executeMigration(ctx context.Context, decision ReschedulingDecision) error {
	r.logger.Info("开始执行Pod迁移",
		"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
		"sourceNode", decision.SourceNode,
		"targetNode", decision.TargetNode,
		"reason", decision.Reason,
		"strategy", decision.Strategy)

	// 1. 创建Pod的副本到目标节点
	newPod := decision.Pod.DeepCopy()
	newPod.ResourceVersion = ""
	newPod.UID = ""
	newPod.Name = fmt.Sprintf("%s-migrated-%d", decision.Pod.Name, time.Now().Unix())
	newPod.Spec.NodeName = decision.TargetNode
	newPod.Status = v1.PodStatus{}

	// 添加迁移标签
	if newPod.Labels == nil {
		newPod.Labels = make(map[string]string)
	}
	newPod.Labels["scheduler.alpha.kubernetes.io/migrated-from"] = decision.SourceNode
	newPod.Labels["scheduler.alpha.kubernetes.io/migration-reason"] = decision.Strategy

	// 添加迁移注解
	if newPod.Annotations == nil {
		newPod.Annotations = make(map[string]string)
	}
	newPod.Annotations["scheduler.alpha.kubernetes.io/migration-time"] = time.Now().Format(time.RFC3339)
	newPod.Annotations["scheduler.alpha.kubernetes.io/original-pod"] = string(decision.Pod.UID)

	// 2. 创建新Pod
	_, err := r.clientset.CoreV1().Pods(newPod.Namespace).Create(ctx, newPod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("创建迁移Pod失败: %v", err)
	}

	r.logger.Info("成功创建迁移Pod", "newPod", fmt.Sprintf("%s/%s", newPod.Namespace, newPod.Name))

	// 3. 等待新Pod运行，然后删除原Pod
	// TODO: 实现更完善的等待和验证逻辑
	go func() {
		time.Sleep(30 * time.Second) // 等待30秒

		// 删除原Pod
		err := r.clientset.CoreV1().Pods(decision.Pod.Namespace).Delete(
			context.Background(),
			decision.Pod.Name,
			metav1.DeleteOptions{})
		if err != nil {
			r.logger.Error(err, "删除原Pod失败", "pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name))
		} else {
			r.logger.Info("成功删除原Pod", "pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name))
		}
	}()

	return nil
}

// Stop 停止重调度器
func (r *Rescheduler) Stop() {
	close(r.stopCh)
}
