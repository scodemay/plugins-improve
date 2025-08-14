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

package helloscheduler

import (
	"context"
	"fmt"
	"math"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// HelloScheduler 是一个简单的调度插件示例
// 它会根据节点名称的字母顺序给节点打分
type HelloScheduler struct {
	handle framework.Handle
	logger klog.Logger
}

// 确保HelloScheduler实现了ScorePlugin接口
var _ framework.ScorePlugin = &HelloScheduler{}

// Name 是插件在注册表和配置中使用的名称
const Name = "HelloScheduler"

// Name 返回插件的名称
func (hs *HelloScheduler) Name() string {
	return Name
}

// Score 在Score扩展点被调用
// 这个函数会为每个节点计算一个分数
func (hs *HelloScheduler) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) (int64, *framework.Status) {
	nodeName := nodeInfo.Node().Name

	// 记录日志，显示我们正在为哪个Pod和节点计算分数
	hs.logger.Info("HelloScheduler正在计算分数",
		"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
		"node", nodeName)

	// 简单的评分策略：根据节点名称的字母顺序
	// 节点名称越靠前（字母顺序），分数越高
	score := int64(0)
	if len(nodeName) > 0 {
		// 使用节点名称第一个字符的ASCII值的反向来计算分数
		// 'a'(97) -> 高分, 'z'(122) -> 低分
		firstChar := nodeName[0]
		score = int64(150 - firstChar) // 这样'a'开头的节点会得到较高分数
	}

	// 添加节点资源状态的考虑
	// 如果节点有足够的可用资源，给额外的分数
	allocatable := nodeInfo.Allocatable
	requested := nodeInfo.Requested

	// 计算CPU使用率
	cpuUsageRatio := float64(requested.MilliCPU) / float64(allocatable.MilliCPU)
	memUsageRatio := float64(requested.Memory) / float64(allocatable.Memory)

	// 资源使用率越低，分数越高
	resourceScore := int64((2.0 - cpuUsageRatio - memUsageRatio) * 50)

	finalScore := score + resourceScore

	hs.logger.Info("HelloScheduler计算完成",
		"node", nodeName,
		"nameScore", score,
		"resourceScore", resourceScore,
		"finalScore", finalScore,
		"cpuUsage", fmt.Sprintf("%.2f%%", cpuUsageRatio*100),
		"memUsage", fmt.Sprintf("%.2f%%", memUsageRatio*100))

	return finalScore, nil
}

// ScoreExtensions 返回ScoreExtensions接口
func (hs *HelloScheduler) ScoreExtensions() framework.ScoreExtensions {
	return hs
}

// NormalizeScore 标准化分数到框架要求的范围内 [0, 100]
func (hs *HelloScheduler) NormalizeScore(ctx context.Context, state *framework.CycleState, pod *v1.Pod, scores framework.NodeScoreList) *framework.Status {
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
	logger := klog.FromContext(ctx).WithName("HelloScheduler")
	logger.Info("HelloScheduler插件正在初始化")

	return &HelloScheduler{
		handle: h,
		logger: logger,
	}, nil
}

