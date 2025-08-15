package Tinyscheduler

import (
	"context"
	"fmt"
	"math"

	v1 "k8s.io/api/core/v1" 
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

//定义一个结构体，将插件所用信息、依赖封装，便于管理
type TinyScheduler struct {
	handle framework.Handle //是一个提供与整个kubernetes框架交互的接口，可获得信息、状态、与其他组件通信的作用
	logger klog.Logger //用于记录日志信息
}

// 确保TinyScheduler实现了ScorePlugin接口
var _ framework.ScorePlugin = &TinyScheduler{} //惯用写法，用来类型检查

// Name 是插件在注册表和配置中使用的名称
const Name = "TinyScheduler"

// Name 返回插件的名称
func (hs *TinyScheduler) Name() string {
	return Name
}

// Score 在Score扩展点被调用
// 这个函数会为每个节点计算一个分数
func (hs *TinyScheduler) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) (int64, *framework.Status) {
	nodeName := nodeInfo.Node().Name

	// 记录日志，显示我们正在为哪个Pod和节点计算分数
	hs.logger.Info("TinyScheduler正在计算分数",
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"node", nodeName)
	

	// 添加节点资源状态的考虑
	// 如果节点有足够的可用资源，给额外的分数
	allocatable := nodeInfo.Allocatable
	requested := nodeInfo.Requested

	// 计算CPU使用率
	cpuUsageRatio := float64(requested.MilliCPU) / float64(allocatable.MilliCPU)
	memUsageRatio := float64(requested.Memory) / float64(allocatable.Memory)

	podCount := len(nodeInfo.Pods)
	loadBalanceScore := int64(110 - podCount*10)

	if loadBalanceScore < 0 {
		loadBalanceScore = 0
	}

	// 资源使用率越低，分数越高
	resourceScore := int64((2.0 - cpuUsageRatio - memUsageRatio) * 50)

	finalScore := resourceScore +loadBalanceScore

	hs.logger.Info("TinyScheduler计算完成",
		"node", nodeName,
		"resourceScore", resourceScore,
		"loadBalanceScore", loadBalanceScore,
		"podCount", podCount,
		"finalScore", finalScore,
		"cpuUsage", fmt.Sprintf("%.2f%%", cpuUsageRatio*100),
		"memUsage", fmt.Sprintf("%.2f%%", memUsageRatio*100))

	return finalScore, nil
}

// ScoreExtensions 返回ScoreExtensions接口
func (hs *TinyScheduler) ScoreExtensions() framework.ScoreExtensions {
	return hs
}

// NormalizeScore 标准化分数到框架要求的范围内 [0, 100]
func (hs *TinyScheduler) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
	// 找到最高分和最低分
	var highest int64 = -math.MaxInt64
	var lowest int64 = math.MaxInt64

	for _, nodeScore := range scores {
		if nodeScore.Score > highest {
			highest = nodeScore.Score
		}
		if nodeScore.Score < lowest {
			lowest = nodeScore.Score
		}
	}

	hs.logger.Info("分数标准化",
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"highest", highest,
		"lowest", lowest)

	// 将分数范围转换为框架要求的 [MinNodeScore, MaxNodeScore] 范围
	oldRange := highest - lowest
	newRange := framework.MaxNodeScore - framework.MinNodeScore

	for i, nodeScore := range scores {
		if oldRange == 0 {
			// 如果所有节点分数相同，设置为最小分数
			scores[i].Score = framework.MinNodeScore
		} else {
			// 线性转换到新的分数范围
			scores[i].Score = ((nodeScore.Score - lowest) * newRange / oldRange) + framework.MinNodeScore
		}

		hs.logger.Info("节点最终分数",
			"node", scores[i].Name,
			"originalScore", nodeScore.Score,
			"normalizedScore", scores[i].Score)
	}

	return nil
}

// New 初始化一个新的插件实例并返回
func New(ctx context.Context, obj runtime.Object, h framework.Handle) (framework.Plugin, error) {
	logger := klog.FromContext(ctx).WithName("TinyScheduler")
	logger.Info("TinyScheduler插件正在初始化")

	return &TinyScheduler{
		handle: h,
		logger: logger,
	}, nil
}

