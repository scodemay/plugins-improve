package rescheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	corelisters "k8s.io/client-go/listers/core/v1"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	metricsClient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	// Name 是重调度器插件在注册表和配置中使用的名称
	Name = "Rescheduler"

	// 默认配置
	DefaultCPUThreshold    = 80.0 // CPU使用率阈值
	DefaultMemoryThreshold = 80.0 // 内存使用率阈值

	// 调度优化默认配置
	DefaultCPUScoreWeight    = 0.6
	DefaultMemoryScoreWeight = 0.4
	DefaultLoadBalanceBonus  = 10.0
)

// ReschedulerConfig 重调度器配置
type ReschedulerConfig struct {
	// CPU使用率阈值 (%)
	CPUThreshold float64 `json:"cpuThreshold,omitempty"`

	// 内存使用率阈值 (%)
	MemoryThreshold float64 `json:"memoryThreshold,omitempty"`

	// 排除的命名空间
	ExcludedNamespaces []string `json:"excludedNamespaces,omitempty"`

	// 调度优化配置
	EnableSchedulingOptimization bool    `json:"enableSchedulingOptimization,omitempty"`
	EnablePreventiveRescheduling bool    `json:"enablePreventiveRescheduling,omitempty"`
	CPUScoreWeight               float64 `json:"cpuScoreWeight,omitempty"`
	MemoryScoreWeight            float64 `json:"memoryScoreWeight,omitempty"`
	LoadBalanceBonus             float64 `json:"loadBalanceBonus,omitempty"`

	// 控制器配置
	EnableReschedulingController bool   `json:"enableReschedulingController,omitempty"`
	ReschedulingInterval         string `json:"reschedulingInterval,omitempty"`
}

// Rescheduler 重调度器结构体
type Rescheduler struct {
	logger     klog.Logger
	handle     framework.Handle
	config     *ReschedulerConfig
	podLister  corelisters.PodLister
	nodeLister corelisters.NodeLister

	// 重调度控制器
	controller *ReschedulingController

	// 资源预留管理器
	reservationManager *ReservationManager

	// 资源计算器
	resourceCalculator *ResourceCalculator
}

// ResourceRequests 封装资源请求信息
type ResourceRequests struct {
	CPU    int64 // 毫核
	Memory int64 // 字节
}

// NodeCapacity 封装节点容量信息
type NodeCapacity struct {
	CPU    int64 // 毫核
	Memory int64 // 字节
}

// ResourceUsagePercent 封装资源使用率
type ResourceUsagePercent struct {
	CPU    float64
	Memory float64
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

// ============================= 资源计算器 =============================

// ResourceCalculator 资源计算器 - 封装所有资源计算逻辑
type ResourceCalculator struct {
	config *ReschedulerConfig // 添加配置引用，统一使用权重和阈值
}

// NewResourceCalculator 创建资源计算器
func NewResourceCalculator(config *ReschedulerConfig) *ResourceCalculator {
	return &ResourceCalculator{
		config: config,
	}
}

// CalculatePodResourceRequests 计算Pod的资源需求
func (rc *ResourceCalculator) CalculatePodResourceRequests(pod *v1.Pod) ResourceRequests {
	var cpuRequests, memoryRequests int64

	for _, container := range pod.Spec.Containers {
		if cpu := container.Resources.Requests.Cpu(); cpu != nil {
			cpuRequests += cpu.MilliValue()
		}
		if memory := container.Resources.Requests.Memory(); memory != nil {
			memoryRequests += memory.Value()
		}
	}

	return ResourceRequests{
		CPU:    cpuRequests,
		Memory: memoryRequests,
	}
}

// GetNodeCapacity 获取节点容量
func (rc *ResourceCalculator) GetNodeCapacity(node *v1.Node) NodeCapacity {
	var cpuCapacity, memoryCapacity int64

	if cpu := node.Status.Capacity.Cpu(); cpu != nil {
		cpuCapacity = cpu.MilliValue()
	}
	if memory := node.Status.Capacity.Memory(); memory != nil {
		memoryCapacity = memory.Value()
	}

	return NodeCapacity{
		CPU:    cpuCapacity,
		Memory: memoryCapacity,
	}
}

// CalculateUsagePercent 计算资源使用率百分比
func (rc *ResourceCalculator) CalculateUsagePercent(requests ResourceRequests, capacity NodeCapacity) ResourceUsagePercent {
	var cpuPercent, memoryPercent float64

	if capacity.CPU > 0 {
		cpuPercent = float64(requests.CPU) / float64(capacity.CPU) * 100.0
	}
	if capacity.Memory > 0 {
		memoryPercent = float64(requests.Memory) / float64(capacity.Memory) * 100.0
	}

	return ResourceUsagePercent{
		CPU:    cpuPercent,
		Memory: memoryPercent,
	}
}

// CalculateScore 计算综合分数 - 统一使用配置权重
func (rc *ResourceCalculator) CalculateScore(usage ResourceUsagePercent) float64 {
	// 使用配置的权重计算分数
	cpuScore := (100.0 - usage.CPU) * rc.config.CPUScoreWeight
	memoryScore := (100.0 - usage.Memory) * rc.config.MemoryScoreWeight
	totalScore := cpuScore + memoryScore

	// 低负载奖励
	if usage.CPU < 30 && usage.Memory < 30 {
		totalScore += rc.config.LoadBalanceBonus
	}

	return totalScore
}

// IsOverloaded 检查节点是否过载 - 统一阈值检查逻辑
func (rc *ResourceCalculator) IsOverloaded(usage ResourceUsagePercent) (bool, string) {
	var reasons []string

	if usage.CPU > rc.config.CPUThreshold {
		reasons = append(reasons, fmt.Sprintf("CPU使用率%.1f%%超过阈值%.1f%%", usage.CPU, rc.config.CPUThreshold))
	}

	if usage.Memory > rc.config.MemoryThreshold {
		reasons = append(reasons, fmt.Sprintf("内存使用率%.1f%%超过阈值%.1f%%", usage.Memory, rc.config.MemoryThreshold))
	}

	if len(reasons) > 0 {
		return true, fmt.Sprintf("节点过载: %v", reasons)
	}

	return false, ""
}

// CalculateUsageFromMetrics 从真实指标计算使用率 - 统一指标转换逻辑
func (rc *ResourceCalculator) CalculateUsageFromMetrics(cpuUsage, memoryUsage, cpuCapacity, memoryCapacity resource.Quantity) ResourceUsagePercent {
	var cpuPercent, memoryPercent float64

	if !cpuCapacity.IsZero() {
		cpuPercent = float64(cpuUsage.MilliValue()) / float64(cpuCapacity.MilliValue()) * 100.0
	}

	if !memoryCapacity.IsZero() {
		memoryPercent = float64(memoryUsage.Value()) / float64(memoryCapacity.Value()) * 100.0
	}

	return ResourceUsagePercent{
		CPU:    cpuPercent,
		Memory: memoryPercent,
	}
}

// CalculateNodeScoreFromMetrics 直接从指标计算节点分数 - 统一评分逻辑
func (rc *ResourceCalculator) CalculateNodeScoreFromMetrics(cpuPercent, memoryPercent float64) float64 {
	usage := ResourceUsagePercent{
		CPU:    cpuPercent,
		Memory: memoryPercent,
	}
	return rc.CalculateScore(usage)
}

// ReservationInfo 资源预留信息
type ReservationInfo struct {
	PodKey         string // pod的唯一标识，格式为 "namespace/name"
	NodeName       string // 预留的节点名称
	CPUReserved    int64  // 预留的CPU资源 (毫核)
	MemoryReserved int64  // 预留的内存资源 (字节)
	Timestamp      int64  // 预留时间戳
}

// ReservationManager 资源预留管理器
type ReservationManager struct {
	mu           sync.RWMutex
	reservations map[string]*ReservationInfo // key: podKey, value: ReservationInfo
}

// 确保Rescheduler实现了相关接口
var _ framework.Plugin = &Rescheduler{}
var _ framework.FilterPlugin = &Rescheduler{}
var _ framework.ScorePlugin = &Rescheduler{}
var _ framework.ReservePlugin = &Rescheduler{}
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
		CPUThreshold:                 DefaultCPUThreshold,
		MemoryThreshold:              DefaultMemoryThreshold,
		ExcludedNamespaces:           []string{"kube-system"},
		EnableSchedulingOptimization: true,
		EnablePreventiveRescheduling: true,
		CPUScoreWeight:               DefaultCPUScoreWeight,
		MemoryScoreWeight:            DefaultMemoryScoreWeight,
		LoadBalanceBonus:             DefaultLoadBalanceBonus,
		EnableReschedulingController: true,
		ReschedulingInterval:         "30s",
	}

	// 从obj中解析实际配置
	if obj != nil {
		if err := parsePluginConfig(obj, config, logger); err != nil {
			logger.Error(err, "解析插件配置失败，使用默认配置")
		}
	}

	rescheduler := &Rescheduler{
		logger:     logger,
		handle:     h,
		config:     config,
		podLister:  h.SharedInformerFactory().Core().V1().Pods().Lister(),
		nodeLister: h.SharedInformerFactory().Core().V1().Nodes().Lister(),
		reservationManager: &ReservationManager{
			reservations: make(map[string]*ReservationInfo),
		},
		resourceCalculator: NewResourceCalculator(config),
	}

	// 初始化重调度控制器（如果启用）
	if config.EnableReschedulingController {
		logger.Info("初始化重调度控制器")

		// 创建Metrics客户端
		metricsClient, err := metricsClient.NewForConfig(h.KubeConfig())
		if err != nil {
			logger.Error(err, "创建Metrics客户端失败，禁用重调度控制器")
		} else {
			controller := NewReschedulingController(
				h.ClientSet(),
				metricsClient,
				h.SharedInformerFactory().Core().V1().Pods().Lister(),
				h.SharedInformerFactory().Core().V1().Nodes().Lister(),
				config,
			)

			rescheduler.controller = controller

			// 启动控制器
			go func() {
				logger.Info("启动重调度控制器")
				if err := controller.Run(ctx); err != nil {
					logger.Error(err, "重调度控制器运行失败")
				}
			}()
		}
	}

	return rescheduler, nil
}

// Reserve 实现ReservePlugin接口 - 资源预留机制
func (r *Rescheduler) Reserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	if !r.config.EnableSchedulingOptimization {
		return nil // 如果未启用优化，直接通过
	}

	// 获取节点信息
	node, err := r.nodeLister.Get(nodeName)
	if err != nil {
		return framework.AsStatus(fmt.Errorf("获取节点信息失败: %v", err))
	}

	// 计算Pod的资源需求
	podRequests := r.resourceCalculator.CalculatePodResourceRequests(pod)

	// 预测资源使用情况（考虑现有预留资源）
	predictedUsage := r.predictNodeUsageAfterPodSchedulingWithReservations(node, pod)

	// 记录资源预留信息
	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	r.reservationManager.Reserve(podKey, nodeName, podRequests.CPU, podRequests.Memory)

	r.logger.V(3).Info("资源预留成功",
		"pod", podKey,
		"node", nodeName,
		"cpuReserved", podRequests.CPU,
		"memoryReserved", podRequests.Memory,
		"predictedCPU", predictedUsage.CPUUsagePercent,
		"predictedMemory", predictedUsage.MemoryUsagePercent)

	// 检查预留后是否会导致严重过载
	if predictedUsage.CPUUsagePercent > 95.0 || predictedUsage.MemoryUsagePercent > 95.0 {
		// 如果过载，需要取消预留
		r.reservationManager.Unreserve(podKey)
		return framework.NewStatus(framework.Unschedulable,
			fmt.Sprintf("预留资源后节点将严重过载 CPU:%.1f%% Memory:%.1f%%",
				predictedUsage.CPUUsagePercent, predictedUsage.MemoryUsagePercent))
	}

	return nil
}

// Unreserve 实现ReservePlugin接口 - 释放资源预留
func (r *Rescheduler) Unreserve(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) {
	if !r.config.EnableSchedulingOptimization {
		return
	}

	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	// 释放资源预留
	reservation := r.reservationManager.Unreserve(podKey)

	if reservation != nil {
		r.logger.V(3).Info("资源预留释放成功",
			"pod", podKey,
			"node", nodeName,
			"cpuReleased", reservation.CPUReserved,
			"memoryReleased", reservation.MemoryReserved,
			"reason", "调度失败或取消")
	} else {
		r.logger.V(4).Info("未找到对应的资源预留记录",
			"pod", podKey,
			"node", nodeName,
			"reason", "可能已被释放或未曾预留")
	}
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

		// 使用资源计算器计算Pod资源需求
		podRequests := r.resourceCalculator.CalculatePodResourceRequests(pod)
		usage.CPURequests += podRequests.CPU
		usage.MemoryRequests += podRequests.Memory
	}

	// 计算资源使用百分比
	for _, usage := range nodeUsages {
		node := usage.Node

		// 使用资源计算器获取节点容量
		capacity := r.resourceCalculator.GetNodeCapacity(node)

		// 计算使用率
		requests := ResourceRequests{
			CPU:    usage.CPURequests,
			Memory: usage.MemoryRequests,
		}
		usagePercent := r.resourceCalculator.CalculateUsagePercent(requests, capacity)

		usage.CPUUsagePercent = usagePercent.CPU
		usage.MemoryUsagePercent = usagePercent.Memory
		usage.Score = r.resourceCalculator.CalculateScore(usagePercent)
	}

	return nodeUsages
}

// Filter 实现FilterPlugin接口 - 在调度新Pod时过滤过载节点
func (r *Rescheduler) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	if !r.config.EnableSchedulingOptimization {
		return nil // 如果未启用调度优化，直接通过
	}

	// 获取当前节点资源使用情况
	nodeUsage := r.getNodeUsage(nodeInfo.Node())

	// 使用统一的过载检查逻辑
	usage := ResourceUsagePercent{
		CPU:    nodeUsage.CPUUsagePercent,
		Memory: nodeUsage.MemoryUsagePercent,
	}

	if isOverloaded, reason := r.resourceCalculator.IsOverloaded(usage); isOverloaded {
		return framework.NewStatus(framework.Unschedulable, reason)
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

	// 使用统一的评分逻辑
	usage := ResourceUsagePercent{
		CPU:    nodeUsage.CPUUsagePercent,
		Memory: nodeUsage.MemoryUsagePercent,
	}

	totalScore := r.resourceCalculator.CalculateScore(usage)
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

// PreBind 增强实现 - 在绑定新Pod前进行预测检查和日志记录
func (r *Rescheduler) PreBind(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) *framework.Status {
	if !r.config.EnablePreventiveRescheduling {
		return nil // 如果未启用预测检查，直接通过
	}

	// 获取目标节点的当前状态
	node, err := r.nodeLister.Get(nodeName)
	if err != nil {
		return framework.AsStatus(fmt.Errorf("获取节点信息失败: %v", err))
	}

	// 预测Pod调度后的节点使用情况
	predictedUsage := r.predictNodeUsageAfterPodScheduling(node, pod, false)

	r.logger.Info("Pod调度预测分析",
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"targetNode", nodeName,
		"predictedCPU", predictedUsage.CPUUsagePercent,
		"predictedMemory", predictedUsage.MemoryUsagePercent)

	// 使用统一的过载检查逻辑进行预警
	usage := ResourceUsagePercent{
		CPU:    predictedUsage.CPUUsagePercent,
		Memory: predictedUsage.MemoryUsagePercent,
	}

	if isOverloaded, reason := r.resourceCalculator.IsOverloaded(usage); isOverloaded {
		r.logger.Info("节点资源使用率预警",
			"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
			"targetNode", nodeName,
			"predictedCPU", predictedUsage.CPUUsagePercent,
			"predictedMemory", predictedUsage.MemoryUsagePercent,
			"reason", reason)
	}

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

// getNodeUsageWithReservations 获取节点实时使用情况（包含预留资源）
func (r *Rescheduler) getNodeUsageWithReservations(node *v1.Node) *NodeResourceUsage {
	// 获取基础使用情况
	baseUsage := r.getNodeUsage(node)

	// 获取预留资源
	reservedCPU, reservedMemory := r.reservationManager.GetTotalReservedResources(node.Name)

	// 计算包含预留资源的总请求量
	totalRequests := ResourceRequests{
		CPU:    baseUsage.CPURequests + reservedCPU,
		Memory: baseUsage.MemoryRequests + reservedMemory,
	}

	// 使用资源计算器获取节点容量和计算使用率
	capacity := r.resourceCalculator.GetNodeCapacity(node)
	usage := r.resourceCalculator.CalculateUsagePercent(totalRequests, capacity)

	return &NodeResourceUsage{
		Node:               node,
		CPUUsagePercent:    usage.CPU,
		MemoryUsagePercent: usage.Memory,
		CPURequests:        totalRequests.CPU,
		MemoryRequests:     totalRequests.Memory,
		PodCount:           baseUsage.PodCount,
		Score:              r.resourceCalculator.CalculateScore(usage),
	}
}

// predictNodeUsageAfterPodScheduling 统一的预测方法
func (r *Rescheduler) predictNodeUsageAfterPodScheduling(node *v1.Node, newPod *v1.Pod, includeReservations bool) *NodeResourceUsage {
	var currentUsage *NodeResourceUsage
	if includeReservations {
		currentUsage = r.getNodeUsageWithReservations(node)
	} else {
		currentUsage = r.getNodeUsage(node)
	}

	// 使用资源计算器计算新Pod的资源需求
	podRequests := r.resourceCalculator.CalculatePodResourceRequests(newPod)

	// 计算预测的总请求量
	predictedRequests := ResourceRequests{
		CPU:    currentUsage.CPURequests + podRequests.CPU,
		Memory: currentUsage.MemoryRequests + podRequests.Memory,
	}

	// 使用资源计算器计算预测使用率
	capacity := r.resourceCalculator.GetNodeCapacity(node)
	usage := r.resourceCalculator.CalculateUsagePercent(predictedRequests, capacity)

	return &NodeResourceUsage{
		Node:               node,
		CPUUsagePercent:    usage.CPU,
		MemoryUsagePercent: usage.Memory,
		CPURequests:        predictedRequests.CPU,
		MemoryRequests:     predictedRequests.Memory,
		PodCount:           currentUsage.PodCount + 1,
		Score:              r.resourceCalculator.CalculateScore(usage),
	}
}

// predictNodeUsageAfterPodSchedulingWithReservations 兼容性方法（考虑预留资源）
func (r *Rescheduler) predictNodeUsageAfterPodSchedulingWithReservations(node *v1.Node, newPod *v1.Pod) *NodeResourceUsage {
	return r.predictNodeUsageAfterPodScheduling(node, newPod, true)
}

// ReschedulerArgs 插件配置参数结构体（支持JSON和YAML）
type ReschedulerArgs struct {
	CPUThreshold                 float64  `json:"cpuThreshold,omitempty" yaml:"cpuThreshold,omitempty"`
	MemoryThreshold              float64  `json:"memoryThreshold,omitempty" yaml:"memoryThreshold,omitempty"`
	ExcludedNamespaces           []string `json:"excludedNamespaces,omitempty" yaml:"excludedNamespaces,omitempty"`
	EnableSchedulingOptimization *bool    `json:"enableSchedulingOptimization,omitempty" yaml:"enableSchedulingOptimization,omitempty"` // 指针类型支持三态
	EnablePreventiveRescheduling *bool    `json:"enablePreventiveRescheduling,omitempty" yaml:"enablePreventiveRescheduling,omitempty"` // 指针类型支持三态
	CPUScoreWeight               float64  `json:"cpuScoreWeight,omitempty" yaml:"cpuScoreWeight,omitempty"`
	MemoryScoreWeight            float64  `json:"memoryScoreWeight,omitempty" yaml:"memoryScoreWeight,omitempty"`
	LoadBalanceBonus             float64  `json:"loadBalanceBonus,omitempty" yaml:"loadBalanceBonus,omitempty"`
	EnableReschedulingController *bool    `json:"enableReschedulingController,omitempty" yaml:"enableReschedulingController,omitempty"`
	ReschedulingInterval         string   `json:"reschedulingInterval,omitempty" yaml:"reschedulingInterval,omitempty"`
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

	// 排除的命名空间
	if len(args.ExcludedNamespaces) > 0 {
		config.ExcludedNamespaces = args.ExcludedNamespaces
		logger.V(2).Info("设置排除命名空间", "namespaces", args.ExcludedNamespaces)
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

	// 重调度控制器开关
	if args.EnableReschedulingController != nil {
		config.EnableReschedulingController = *args.EnableReschedulingController
		logger.V(2).Info("设置重调度控制器开关", "enabled", *args.EnableReschedulingController)
	}

	// 重调度间隔
	if args.ReschedulingInterval != "" {
		config.ReschedulingInterval = args.ReschedulingInterval
		logger.V(2).Info("设置重调度间隔", "interval", args.ReschedulingInterval)
	}

	// 验证配置的完整性和合理性
	return validateConfig(config, logger)
}

// validateConfig 验证配置的合理性
func validateConfig(config *ReschedulerConfig, logger klog.Logger) error {
	// 验证阈值范围
	if config.CPUThreshold <= 0 || config.CPUThreshold > 100 {
		return fmt.Errorf("CPU阈值必须在0-100之间: %v", config.CPUThreshold)
	}
	if config.MemoryThreshold <= 0 || config.MemoryThreshold > 100 {
		return fmt.Errorf("内存阈值必须在0-100之间: %v", config.MemoryThreshold)
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
	return fmt.Sprintf("cpuThreshold=%.1f%%, memoryThreshold=%.1f%%, schedulingOpt=%v, preventive=%v, controller=%v",
		config.CPUThreshold,
		config.MemoryThreshold,
		config.EnableSchedulingOptimization,
		config.EnablePreventiveRescheduling,
		config.EnableReschedulingController)
}

// Stop 停止重调度器
func (r *Rescheduler) Stop() {
	if r.controller != nil {
		r.controller.Stop()
	}
}

// Reserve 预留资源
func (rm *ReservationManager) Reserve(podKey, nodeName string, cpuReserved, memoryReserved int64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.reservations[podKey] = &ReservationInfo{
		PodKey:         podKey,
		NodeName:       nodeName,
		CPUReserved:    cpuReserved,
		MemoryReserved: memoryReserved,
		Timestamp:      getCurrentTimestamp(),
	}
}

// Unreserve 释放资源预留
func (rm *ReservationManager) Unreserve(podKey string) *ReservationInfo {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if reservation, exists := rm.reservations[podKey]; exists {
		delete(rm.reservations, podKey)
		return reservation
	}
	return nil
}

// GetReservation 获取指定Pod的预留信息
func (rm *ReservationManager) GetReservation(podKey string) *ReservationInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if reservation, exists := rm.reservations[podKey]; exists {
		return reservation
	}
	return nil
}

// GetNodeReservations 获取指定节点上的所有预留信息
func (rm *ReservationManager) GetNodeReservations(nodeName string) []*ReservationInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var reservations []*ReservationInfo
	for _, reservation := range rm.reservations {
		if reservation.NodeName == nodeName {
			reservations = append(reservations, reservation)
		}
	}
	return reservations
}

// GetTotalReservedResources 获取指定节点上的总预留资源
func (rm *ReservationManager) GetTotalReservedResources(nodeName string) (cpuReserved, memoryReserved int64) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	for _, reservation := range rm.reservations {
		if reservation.NodeName == nodeName {
			cpuReserved += reservation.CPUReserved
			memoryReserved += reservation.MemoryReserved
		}
	}
	return
}

// CleanupExpiredReservations 清理过期的预留（可选：防止内存泄漏）
func (rm *ReservationManager) CleanupExpiredReservations(expirationSeconds int64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	currentTime := getCurrentTimestamp()
	for podKey, reservation := range rm.reservations {
		if currentTime-reservation.Timestamp > expirationSeconds {
			delete(rm.reservations, podKey)
		}
	}
}

// getCurrentTimestamp 获取当前时间戳
func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}
