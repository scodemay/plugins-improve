package rescheduler

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

// DeploymentCoordinator 实现与Deployment Controller协调的重调度
type DeploymentCoordinator struct {
	clientset        kubernetes.Interface
	deploymentLister appslisters.DeploymentLister
	replicaSetLister appslisters.ReplicaSetLister
	podLister        corelisters.PodLister
	logger           klog.Logger
}

// NewDeploymentCoordinator 创建Deployment协调器
func NewDeploymentCoordinator(
	clientset kubernetes.Interface,
	deploymentLister appslisters.DeploymentLister,
	replicaSetLister appslisters.ReplicaSetLister,
	podLister corelisters.PodLister,
) *DeploymentCoordinator {
	return &DeploymentCoordinator{
		clientset:        clientset,
		deploymentLister: deploymentLister,
		replicaSetLister: replicaSetLister,
		podLister:        podLister,
		logger:           klog.FromContext(context.Background()).WithName("deployment-coordinator"),
	}
}

// CoordinatedRescheduling 协调式重调度 - 避免与Deployment Controller冲突
func (dc *DeploymentCoordinator) CoordinatedRescheduling(ctx context.Context, decision ReschedulingDecision) error {
	// 第1步：检查Pod是否属于Deployment
	deployment, replicaSet, err := dc.findOwnerDeployment(ctx, decision.Pod)
	if err != nil {
		return fmt.Errorf("查找Pod所属Deployment失败: %v", err)
	}

	if deployment != nil {
		// 策略A：Deployment管理的Pod - 使用优雅的滚动更新
		return dc.deploymentBasedRescheduling(ctx, decision, deployment, replicaSet)
	} else {
		// 策略B：独立Pod - 使用原有的直接迁移（但改进命名）
		return dc.standalonePodRescheduling(ctx, decision)
	}
}

// deploymentBasedRescheduling 基于Deployment的重调度策略
func (dc *DeploymentCoordinator) deploymentBasedRescheduling(ctx context.Context, decision ReschedulingDecision,
	deployment *appsv1.Deployment, _ *appsv1.ReplicaSet) error {

	dc.logger.Info("开始Deployment协调重调度",
		"deployment", fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name),
		"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
		"sourceNode", decision.SourceNode,
		"targetNode", decision.TargetNode)

	// 方法1：Pod驱逐 + NodeAffinity引导
	return dc.gracefulEvictionWithNodeGuidance(ctx, decision, deployment)
}

// gracefulEvictionWithNodeGuidance 优雅驱逐 + 节点引导策略
func (dc *DeploymentCoordinator) gracefulEvictionWithNodeGuidance(ctx context.Context,
	decision ReschedulingDecision, deployment *appsv1.Deployment) error {

	// 第1步：临时修改Deployment的Pod分布偏好
	err := dc.addNodePreference(ctx, deployment, decision.TargetNode, decision.SourceNode)
	if err != nil {
		return fmt.Errorf("添加节点偏好失败: %v", err)
	}

	// 第2步：创建PodDisruptionBudget确保服务稳定性
	pdbName := fmt.Sprintf("%s-rescheduler-pdb", deployment.Name)
	err = dc.createTemporaryPDB(ctx, deployment.Namespace, pdbName, deployment.Spec.Selector)
	if err != nil {
		dc.logger.Error(err, "创建临时PDB失败", "pdb", pdbName)
	}

	// 第3步：优雅驱逐Pod - 让Deployment Controller自动重建
	err = dc.gracefullyEvictPod(ctx, decision.Pod)
	if err != nil {
		// 回滚节点偏好设置
		dc.removeNodePreference(ctx, deployment, decision.TargetNode)
		return fmt.Errorf("优雅驱逐Pod失败: %v", err)
	}

	// 第4步：异步等待重建完成，然后清理临时设置
	go dc.waitAndCleanup(ctx, deployment, decision.TargetNode, pdbName)

	dc.logger.Info("成功启动协调重调度",
		"deployment", fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name),
		"evictedPod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name))

	return nil
}

// addNodePreference 添加节点偏好到Deployment
func (dc *DeploymentCoordinator) addNodePreference(ctx context.Context, deployment *appsv1.Deployment,
	preferredNode, avoidNode string) error {

	deploymentCopy := deployment.DeepCopy()

	// 添加NodeAffinity偏好目标节点，避免源节点
	if deploymentCopy.Spec.Template.Spec.Affinity == nil {
		deploymentCopy.Spec.Template.Spec.Affinity = &v1.Affinity{}
	}
	if deploymentCopy.Spec.Template.Spec.Affinity.NodeAffinity == nil {
		deploymentCopy.Spec.Template.Spec.Affinity.NodeAffinity = &v1.NodeAffinity{}
	}

	// 添加偏好调度到目标节点
	preferredScheduling := []v1.PreferredSchedulingTerm{
		{
			Weight: 100,
			Preference: v1.NodeSelectorTerm{
				MatchExpressions: []v1.NodeSelectorRequirement{
					{
						Key:      "kubernetes.io/hostname",
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{preferredNode},
					},
				},
			},
		},
		{
			Weight: -50, // 负权重：避免源节点
			Preference: v1.NodeSelectorTerm{
				MatchExpressions: []v1.NodeSelectorRequirement{
					{
						Key:      "kubernetes.io/hostname",
						Operator: v1.NodeSelectorOpIn,
						Values:   []string{avoidNode},
					},
				},
			},
		},
	}

	deploymentCopy.Spec.Template.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = preferredScheduling

	// 添加临时注解标记这是重调度引起的更改
	if deploymentCopy.Annotations == nil {
		deploymentCopy.Annotations = make(map[string]string)
	}
	deploymentCopy.Annotations["scheduler.alpha.kubernetes.io/rescheduler-modified"] = time.Now().Format(time.RFC3339)
	deploymentCopy.Annotations["scheduler.alpha.kubernetes.io/preferred-node"] = preferredNode
	deploymentCopy.Annotations["scheduler.alpha.kubernetes.io/avoid-node"] = avoidNode

	_, err := dc.clientset.AppsV1().Deployments(deployment.Namespace).Update(ctx, deploymentCopy, metav1.UpdateOptions{})
	return err
}

// createTemporaryPDB 创建临时的PodDisruptionBudget
func (dc *DeploymentCoordinator) createTemporaryPDB(ctx context.Context, namespace, name string,
	selector *metav1.LabelSelector) error {

	minAvailable := intstr.FromInt(1)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"scheduler.alpha.kubernetes.io/created-by": "rescheduler",
				"scheduler.alpha.kubernetes.io/temporary":  "true",
			},
			Annotations: map[string]string{
				"scheduler.alpha.kubernetes.io/created-at": time.Now().Format(time.RFC3339),
				"scheduler.alpha.kubernetes.io/ttl":        "300", // 5分钟TTL
			},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector:     selector,
		},
	}

	_, err := dc.clientset.PolicyV1().PodDisruptionBudgets(namespace).Create(ctx, pdb, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("创建PDB失败: %v", err)
	}

	return nil
}

// gracefullyEvictPod 优雅驱逐Pod
func (dc *DeploymentCoordinator) gracefullyEvictPod(ctx context.Context, pod *v1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}

	return dc.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
}

// waitAndCleanup 等待重建完成并清理临时设置
func (dc *DeploymentCoordinator) waitAndCleanup(ctx context.Context, deployment *appsv1.Deployment,
	preferredNode, pdbName string) {

	// 等待5分钟让Pod重建完成
	time.Sleep(5 * time.Minute)

	// 清理节点偏好设置
	err := dc.removeNodePreference(ctx, deployment, preferredNode)
	if err != nil {
		dc.logger.Error(err, "清理节点偏好失败", "deployment", deployment.Name)
	}

	// 清理临时PDB
	err = dc.clientset.PolicyV1().PodDisruptionBudgets(deployment.Namespace).Delete(
		ctx, pdbName, metav1.DeleteOptions{})
	if err != nil {
		dc.logger.Error(err, "清理临时PDB失败", "pdb", pdbName)
	}

	dc.logger.Info("完成协调重调度清理", "deployment", deployment.Name)
}

// removeNodePreference 移除节点偏好设置
func (dc *DeploymentCoordinator) removeNodePreference(ctx context.Context, deployment *appsv1.Deployment, _ string) error {
	currentDeployment, err := dc.clientset.AppsV1().Deployments(deployment.Namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	deploymentCopy := currentDeployment.DeepCopy()

	// 移除NodeAffinity设置
	if deploymentCopy.Spec.Template.Spec.Affinity != nil &&
		deploymentCopy.Spec.Template.Spec.Affinity.NodeAffinity != nil {
		deploymentCopy.Spec.Template.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = nil
	}

	// 移除重调度相关的注解
	if deploymentCopy.Annotations != nil {
		delete(deploymentCopy.Annotations, "scheduler.alpha.kubernetes.io/rescheduler-modified")
		delete(deploymentCopy.Annotations, "scheduler.alpha.kubernetes.io/preferred-node")
		delete(deploymentCopy.Annotations, "scheduler.alpha.kubernetes.io/avoid-node")
	}

	_, err = dc.clientset.AppsV1().Deployments(deployment.Namespace).Update(ctx, deploymentCopy, metav1.UpdateOptions{})
	return err
}

// standalonePodRescheduling 独立Pod重调度策略（改进版）
func (dc *DeploymentCoordinator) standalonePodRescheduling(ctx context.Context, decision ReschedulingDecision) error {
	// 对于非Deployment管理的Pod，使用改进的命名策略避免冲突
	dc.logger.Info("开始独立Pod重调度",
		"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
		"sourceNode", decision.SourceNode,
		"targetNode", decision.TargetNode)

	// 创建改进的迁移Pod - 使用更友好的命名
	newPod := decision.Pod.DeepCopy()
	newPod.ResourceVersion = ""
	newPod.UID = ""

	// 改进命名：避免与Deployment模式冲突
	timestamp := time.Now().Unix()
	newPod.Name = fmt.Sprintf("%s-rescheduled-%d", decision.Pod.Name, timestamp)
	newPod.Spec.NodeName = decision.TargetNode
	newPod.Status = v1.PodStatus{}

	// 添加明确的标签标识这是重调度Pod
	if newPod.Labels == nil {
		newPod.Labels = make(map[string]string)
	}
	newPod.Labels["scheduler.alpha.kubernetes.io/rescheduled-pod"] = "true"
	newPod.Labels["scheduler.alpha.kubernetes.io/rescheduled-from"] = decision.SourceNode
	newPod.Labels["scheduler.alpha.kubernetes.io/rescheduled-at"] = fmt.Sprintf("%d", timestamp)

	// 创建新Pod
	createdPod, err := dc.clientset.CoreV1().Pods(newPod.Namespace).Create(ctx, newPod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("创建重调度Pod失败: %v", err)
	}

	// 等待新Pod运行后删除原Pod
	go dc.waitAndDeleteOriginalPod(ctx, decision.Pod, createdPod)

	return nil
}

// findOwnerDeployment 查找Pod所属的Deployment
func (dc *DeploymentCoordinator) findOwnerDeployment(_ context.Context, pod *v1.Pod) (*appsv1.Deployment, *appsv1.ReplicaSet, error) {
	// 检查Pod是否由ReplicaSet管理
	for _, ownerRef := range pod.GetOwnerReferences() {
		if ownerRef.Kind == "ReplicaSet" && ownerRef.APIVersion == "apps/v1" {
			// 获取ReplicaSet
			rs, err := dc.replicaSetLister.ReplicaSets(pod.Namespace).Get(ownerRef.Name)
			if err != nil {
				continue
			}

			// 检查ReplicaSet是否由Deployment管理
			for _, rsOwnerRef := range rs.GetOwnerReferences() {
				if rsOwnerRef.Kind == "Deployment" && rsOwnerRef.APIVersion == "apps/v1" {
					deployment, err := dc.deploymentLister.Deployments(pod.Namespace).Get(rsOwnerRef.Name)
					if err != nil {
						continue
					}
					return deployment, rs, nil
				}
			}
		}
	}

	return nil, nil, nil // 不是Deployment管理的Pod
}

// waitAndDeleteOriginalPod 等待新Pod就绪后删除原始Pod
func (dc *DeploymentCoordinator) waitAndDeleteOriginalPod(ctx context.Context, originalPod, newPod *v1.Pod) {
	// 等待新Pod就绪
	for i := 0; i < 60; i++ { // 最多等待5分钟
		updatedPod, err := dc.clientset.CoreV1().Pods(newPod.Namespace).Get(ctx, newPod.Name, metav1.GetOptions{})
		if err != nil {
			dc.logger.Error(err, "获取新Pod状态失败")
			time.Sleep(5 * time.Second)
			continue
		}

		if updatedPod.Status.Phase == v1.PodRunning {
			// 新Pod已运行，删除原始Pod
			err = dc.clientset.CoreV1().Pods(originalPod.Namespace).Delete(ctx, originalPod.Name, metav1.DeleteOptions{})
			if err != nil {
				dc.logger.Error(err, "删除原始Pod失败")
			} else {
				dc.logger.Info("成功完成独立Pod重调度",
					"originalPod", fmt.Sprintf("%s/%s", originalPod.Namespace, originalPod.Name),
					"newPod", fmt.Sprintf("%s/%s", newPod.Namespace, newPod.Name))
			}
			return
		}

		time.Sleep(5 * time.Second)
	}

	dc.logger.Error(nil, "新Pod启动超时，取消重调度",
		"newPod", fmt.Sprintf("%s/%s", newPod.Namespace, newPod.Name))
}
