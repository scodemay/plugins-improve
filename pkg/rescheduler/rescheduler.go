package rescheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	corelisters "k8s.io/client-go/listers/core/v1"

	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
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
}

// Rescheduler 重调度器结构体
type Rescheduler struct {
	logger     klog.Logger
	handle     framework.Handle
	config     *ReschedulerConfig
	podLister  corelisters.PodLister
	nodeLister corelisters.NodeLister
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
		CPUThreshold:                 DefaultCPUThreshold,
		MemoryThreshold:              DefaultMemoryThreshold,
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

	rescheduler := &Rescheduler{
		logger:     logger,
		handle:     h,
		config:     config,
		podLister:  h.SharedInformerFactory().Core().V1().Pods().Lister(),
		nodeLister: h.SharedInformerFactory().Core().V1().Nodes().Lister(),
	}

	return rescheduler, nil
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
	predictedUsage := r.predictNodeUsageAfterPodScheduling(node, pod)

	r.logger.Info("Pod调度预测分析",
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"targetNode", nodeName,
		"predictedCPU", predictedUsage.CPUUsagePercent,
		"predictedMemory", predictedUsage.MemoryUsagePercent)

	// 如果预测调度后节点可能过载，记录警告日志
	if predictedUsage.CPUUsagePercent > r.config.CPUThreshold ||
		predictedUsage.MemoryUsagePercent > r.config.MemoryThreshold {
		r.logger.Info("节点资源使用率预警",
			"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
			"targetNode", nodeName,
			"predictedCPU", predictedUsage.CPUUsagePercent,
			"predictedMemory", predictedUsage.MemoryUsagePercent,
			"cpuThreshold", r.config.CPUThreshold,
			"memoryThreshold", r.config.MemoryThreshold)
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
	return fmt.Sprintf("cpuThreshold=%.1f%%, memoryThreshold=%.1f%%, schedulingOpt=%v, preventive=%v",
		config.CPUThreshold,
		config.MemoryThreshold,
		config.EnableSchedulingOptimization,
		config.EnablePreventiveRescheduling)
}
