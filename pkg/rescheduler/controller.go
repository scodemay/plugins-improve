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
	"time"

	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	policylisters "k8s.io/client-go/listers/policy/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	// 控制器名称
	ControllerName = "rescheduler-controller"

	// 迁移状态标签
	MigrationStatusLabel = "scheduler.alpha.kubernetes.io/migration-status"
	MigrationIDLabel     = "scheduler.alpha.kubernetes.io/migration-id"

	// 迁移状态值
	MigrationStatusPending    = "pending"
	MigrationStatusInProgress = "in-progress"
	MigrationStatusCompleted  = "completed"
	MigrationStatusFailed     = "failed"

	// 重试配置
	DefaultMaxRetries = 5
	DefaultRetryDelay = 10 * time.Second
)

// ReschedulerController 重调度控制器
type ReschedulerController struct {
	logger    klog.Logger
	clientset clientset.Interface

	// Listers
	podLister corelisters.PodLister
	pdbLister policylisters.PodDisruptionBudgetLister
	rsLister  appslisters.ReplicaSetLister

	// Informers
	podInformer cache.SharedIndexInformer

	// 工作队列
	workqueue workqueue.RateLimitingInterface

	// 停止信号
	stopCh chan struct{}
}

// MigrationTask 迁移任务
type MigrationTask struct {
	ID         string    `json:"id"`
	SourcePod  *v1.Pod   `json:"sourcePod"`
	TargetNode string    `json:"targetNode"`
	Strategy   string    `json:"strategy"`
	Reason     string    `json:"reason"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	RetryCount int       `json:"retryCount"`
	LastError  string    `json:"lastError,omitempty"`
	NewPodName string    `json:"newPodName,omitempty"`
}

// NewReschedulerController 创建新的重调度控制器
func NewReschedulerController(
	clientset clientset.Interface,
	informerFactory informers.SharedInformerFactory,
) *ReschedulerController {

	logger := klog.Background().WithName(ControllerName)

	// 获取informers
	podInformer := informerFactory.Core().V1().Pods().Informer()

	controller := &ReschedulerController{
		logger:      logger,
		clientset:   clientset,
		podLister:   informerFactory.Core().V1().Pods().Lister(),
		pdbLister:   informerFactory.Policy().V1().PodDisruptionBudgets().Lister(),
		rsLister:    informerFactory.Apps().V1().ReplicaSets().Lister(),
		podInformer: podInformer,
		workqueue:   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), ControllerName),
		stopCh:      make(chan struct{}),
	}

	// 设置事件处理器
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueuePod,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueuePod(new)
		},
		DeleteFunc: controller.enqueuePod,
	})

	return controller
}

// Run 启动控制器
func (c *ReschedulerController) Run(ctx context.Context, workers int) error {
	defer c.workqueue.ShutDown()

	c.logger.Info("启动重调度控制器", "workers", workers)

	// 等待缓存同步
	c.logger.Info("等待informer缓存同步")
	if !cache.WaitForCacheSync(ctx.Done(), c.podInformer.HasSynced) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	c.logger.Info("缓存同步完成，启动worker")

	// 启动workers
	for i := 0; i < workers; i++ {
		go wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}

	<-ctx.Done()
	c.logger.Info("停止重调度控制器")

	return nil
}

// enqueuePod 将Pod加入工作队列
func (c *ReschedulerController) enqueuePod(obj interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		c.logger.Error(err, "获取对象key失败")
		return
	}

	c.workqueue.Add(key)
}

// runWorker 运行工作协程
func (c *ReschedulerController) runWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

// processNextWorkItem 处理下一个工作项
func (c *ReschedulerController) processNextWorkItem(ctx context.Context) bool {
	obj, shutdown := c.workqueue.Get()
	if shutdown {
		return false
	}

	defer c.workqueue.Done(obj)

	key, ok := obj.(string)
	if !ok {
		c.workqueue.Forget(obj)
		c.logger.Error(nil, "期望字符串类型的key", "key", obj)
		return true
	}

	// 处理Pod
	err := c.syncPod(ctx, key)
	if err == nil {
		c.workqueue.Forget(obj)
		return true
	}

	// 处理错误
	c.logger.Error(err, "处理Pod失败", "key", key)
	if c.workqueue.NumRequeues(obj) < DefaultMaxRetries {
		c.workqueue.AddRateLimited(obj)
		return true
	}

	c.workqueue.Forget(obj)
	c.logger.Error(err, "放弃处理Pod", "key", key)

	return true
}

// syncPod 同步Pod状态
func (c *ReschedulerController) syncPod(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	pod, err := c.podLister.Pods(namespace).Get(name)
	if errors.IsNotFound(err) {
		c.logger.V(4).Info("Pod不存在", "key", key)
		return nil
	}
	if err != nil {
		return err
	}

	// 检查是否是迁移任务相关的Pod
	if migrationID, exists := pod.Labels[MigrationIDLabel]; exists {
		return c.handleMigrationPod(ctx, pod, migrationID)
	}

	return nil
}

// handleMigrationPod 处理迁移相关的Pod
func (c *ReschedulerController) handleMigrationPod(ctx context.Context, pod *v1.Pod, migrationID string) error {
	status, exists := pod.Labels[MigrationStatusLabel]
	if !exists {
		return nil
	}

	switch status {
	case MigrationStatusPending:
		return c.handlePendingMigration(ctx, pod, migrationID)
	case MigrationStatusInProgress:
		return c.handleInProgressMigration(ctx, pod, migrationID)
	case MigrationStatusCompleted:
		return c.handleCompletedMigration(ctx, pod, migrationID)
	case MigrationStatusFailed:
		return c.handleFailedMigration(ctx, pod, migrationID)
	}

	return nil
}

// ExecuteMigration 执行迁移任务 - 这是公开接口，由重调度器调用
func (c *ReschedulerController) ExecuteMigration(ctx context.Context, decision ReschedulingDecision) error {
	migrationID := fmt.Sprintf("migration-%d", time.Now().UnixNano())

	c.logger.Info("开始执行Pod迁移",
		"migrationID", migrationID,
		"pod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
		"sourceNode", decision.SourceNode,
		"targetNode", decision.TargetNode,
		"strategy", decision.Strategy)

	// 第1步：检查PodDisruptionBudget
	if err := c.checkPodDisruptionBudget(ctx, decision.Pod); err != nil {
		return fmt.Errorf("PDB检查失败: %v", err)
	}

	// 第2步：为原Pod添加迁移标签
	if err := c.markPodForMigration(ctx, decision.Pod, migrationID, MigrationStatusPending); err != nil {
		return fmt.Errorf("标记Pod迁移失败: %v", err)
	}

	// 第3步：创建新Pod（目标节点）
	newPod, err := c.createTargetPod(ctx, decision, migrationID)
	if err != nil {
		// 回滚：移除迁移标签
		c.markPodForMigration(ctx, decision.Pod, migrationID, MigrationStatusFailed)
		return fmt.Errorf("创建目标Pod失败: %v", err)
	}

	// 第4步：更新迁移状态为进行中
	if err := c.markPodForMigration(ctx, decision.Pod, migrationID, MigrationStatusInProgress); err != nil {
		c.logger.Error(err, "更新迁移状态失败", "migrationID", migrationID)
	}

	// 第5步：等待新Pod运行
	go c.waitAndEvictSourcePod(ctx, decision.Pod, newPod, migrationID)

	return nil
}

// checkPodDisruptionBudget 检查PodDisruptionBudget
func (c *ReschedulerController) checkPodDisruptionBudget(_ context.Context, pod *v1.Pod) error {
	// 获取所有PDB
	pdbs, err := c.pdbLister.PodDisruptionBudgets(pod.Namespace).List(labels.Everything())
	if err != nil {
		return err
	}

	// 检查Pod是否受PDB保护
	for _, pdb := range pdbs {
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			continue
		}

		if selector.Matches(labels.Set(pod.Labels)) {
			// 检查PDB状态
			if pdb.Status.DisruptionsAllowed <= 0 {
				return fmt.Errorf("PodDisruptionBudget %s/%s 不允许驱逐Pod", pdb.Namespace, pdb.Name)
			}
			c.logger.Info("Pod受PDB保护，但允许驱逐",
				"pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
				"pdb", fmt.Sprintf("%s/%s", pdb.Namespace, pdb.Name),
				"disruptionsAllowed", pdb.Status.DisruptionsAllowed)
		}
	}

	return nil
}

// markPodForMigration 为Pod添加迁移标签 - 改进版避免竞态条件
func (c *ReschedulerController) markPodForMigration(ctx context.Context, pod *v1.Pod, migrationID, status string) error {
	// 使用重试机制避免 "object has been modified" 错误
	return retry(3, 1*time.Second, func() error {
		// 获取最新的Pod状态
		latestPod, err := c.clientset.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		podCopy := latestPod.DeepCopy()

		if podCopy.Labels == nil {
			podCopy.Labels = make(map[string]string)
		}

		podCopy.Labels[MigrationIDLabel] = migrationID
		podCopy.Labels[MigrationStatusLabel] = status

		if podCopy.Annotations == nil {
			podCopy.Annotations = make(map[string]string)
		}
		podCopy.Annotations["scheduler.alpha.kubernetes.io/migration-time"] = time.Now().Format(time.RFC3339)

		// 添加防冲突标记
		podCopy.Annotations["scheduler.alpha.kubernetes.io/rescheduler-processing"] = "true"

		_, err = c.clientset.CoreV1().Pods(pod.Namespace).Update(ctx, podCopy, metav1.UpdateOptions{})
		return err
	})
}

// retry 重试机制
func retry(attempts int, sleep time.Duration, f func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		err = f()
		if err == nil {
			return nil
		}
		if i < attempts-1 {
			time.Sleep(sleep)
		}
	}
	return err
}

// createTargetPod 在目标节点创建新Pod - 改进版避免与Deployment冲突
func (c *ReschedulerController) createTargetPod(ctx context.Context, decision ReschedulingDecision, migrationID string) (*v1.Pod, error) {
	newPod := decision.Pod.DeepCopy()

	// 清除运行时字段
	newPod.ResourceVersion = ""
	newPod.UID = ""

	// 改进命名策略：避免与Deployment Controller冲突
	// 检查是否为Deployment管理的Pod
	isDeploymentPod := c.isDeploymentManagedPod(decision.Pod)
	if isDeploymentPod {
		// 对Deployment管理的Pod使用不同的命名模式
		newPod.Name = fmt.Sprintf("rescheduled-%s-%s", decision.Pod.Name, migrationID[10:20])
	} else {
		// 独立Pod使用原有命名
		newPod.Name = fmt.Sprintf("%s-migrated-%s", decision.Pod.Name, migrationID[10:20])
	}

	newPod.Spec.NodeName = decision.TargetNode
	newPod.Status = v1.PodStatus{}

	// 添加迁移标签
	if newPod.Labels == nil {
		newPod.Labels = make(map[string]string)
	}
	newPod.Labels[MigrationIDLabel] = migrationID
	newPod.Labels[MigrationStatusLabel] = MigrationStatusInProgress
	newPod.Labels["scheduler.alpha.kubernetes.io/migrated-from"] = decision.SourceNode
	newPod.Labels["scheduler.alpha.kubernetes.io/migration-reason"] = decision.Strategy

	// 添加迁移注解
	if newPod.Annotations == nil {
		newPod.Annotations = make(map[string]string)
	}
	newPod.Annotations["scheduler.alpha.kubernetes.io/migration-time"] = time.Now().Format(time.RFC3339)
	newPod.Annotations["scheduler.alpha.kubernetes.io/original-pod"] = string(decision.Pod.UID)
	newPod.Annotations["scheduler.alpha.kubernetes.io/original-pod-name"] = decision.Pod.Name

	// 创建Pod
	createdPod, err := c.clientset.CoreV1().Pods(newPod.Namespace).Create(ctx, newPod, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	c.logger.Info("成功创建目标Pod",
		"originalPod", fmt.Sprintf("%s/%s", decision.Pod.Namespace, decision.Pod.Name),
		"newPod", fmt.Sprintf("%s/%s", createdPod.Namespace, createdPod.Name),
		"targetNode", decision.TargetNode,
		"migrationID", migrationID)

	return createdPod, nil
}

// isDeploymentManagedPod 检查Pod是否由Deployment管理
func (c *ReschedulerController) isDeploymentManagedPod(pod *v1.Pod) bool {
	// 检查Pod的OwnerReferences
	for _, ownerRef := range pod.GetOwnerReferences() {
		if ownerRef.Kind == "ReplicaSet" && ownerRef.APIVersion == "apps/v1" {
			// 进一步检查ReplicaSet是否由Deployment管理
			rs, err := c.rsLister.ReplicaSets(pod.Namespace).Get(ownerRef.Name)
			if err != nil {
				continue
			}
			for _, rsOwnerRef := range rs.GetOwnerReferences() {
				if rsOwnerRef.Kind == "Deployment" && rsOwnerRef.APIVersion == "apps/v1" {
					return true
				}
			}
		}
	}
	return false
}

// waitAndEvictSourcePod 等待新Pod运行后驱逐源Pod
func (c *ReschedulerController) waitAndEvictSourcePod(ctx context.Context, sourcePod, targetPod *v1.Pod, migrationID string) {
	// 等待目标Pod就绪
	err := c.waitForPodReady(ctx, targetPod, 5*time.Minute)
	if err != nil {
		c.logger.Error(err, "等待目标Pod就绪超时", "migrationID", migrationID)
		c.markPodForMigration(ctx, sourcePod, migrationID, MigrationStatusFailed)
		return
	}

	c.logger.Info("目标Pod已就绪，开始驱逐源Pod",
		"migrationID", migrationID,
		"sourcePod", fmt.Sprintf("%s/%s", sourcePod.Namespace, sourcePod.Name),
		"targetPod", fmt.Sprintf("%s/%s", targetPod.Namespace, targetPod.Name))

	// 使用标准Eviction API驱逐源Pod
	err = c.evictPod(ctx, sourcePod)
	if err != nil {
		c.logger.Error(err, "驱逐源Pod失败", "migrationID", migrationID)
		c.markPodForMigration(ctx, sourcePod, migrationID, MigrationStatusFailed)
		return
	}

	c.logger.Info("成功完成Pod迁移",
		"migrationID", migrationID,
		"sourcePod", fmt.Sprintf("%s/%s", sourcePod.Namespace, sourcePod.Name),
		"targetPod", fmt.Sprintf("%s/%s", targetPod.Namespace, targetPod.Name))
}

// waitForPodReady 等待Pod就绪
func (c *ReschedulerController) waitForPodReady(ctx context.Context, pod *v1.Pod, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		currentPod, err := c.clientset.CoreV1().Pods(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		// 检查Pod是否就绪
		for _, condition := range currentPod.Status.Conditions {
			if condition.Type == v1.PodReady && condition.Status == v1.ConditionTrue {
				return true, nil
			}
		}

		// 检查Pod是否失败
		if currentPod.Status.Phase == v1.PodFailed {
			return false, fmt.Errorf("pod进入失败状态: %s", currentPod.Status.Reason)
		}

		return false, nil
	})
}

// evictPod 使用标准Eviction API驱逐Pod
func (c *ReschedulerController) evictPod(ctx context.Context, pod *v1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		DeleteOptions: &metav1.DeleteOptions{
			GracePeriodSeconds: &[]int64{30}[0], // 30秒优雅停止时间
		},
	}

	return c.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
}

// 以下是处理不同迁移状态的方法

func (c *ReschedulerController) handlePendingMigration(_ context.Context, pod *v1.Pod, migrationID string) error {
	c.logger.V(4).Info("处理待处理的迁移", "migrationID", migrationID, "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
	// TODO: 实现待处理迁移的处理逻辑
	return nil
}

func (c *ReschedulerController) handleInProgressMigration(_ context.Context, pod *v1.Pod, migrationID string) error {
	c.logger.V(4).Info("处理进行中的迁移", "migrationID", migrationID, "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
	// TODO: 检查迁移进度，处理超时等情况
	return nil
}

func (c *ReschedulerController) handleCompletedMigration(_ context.Context, pod *v1.Pod, migrationID string) error {
	c.logger.V(4).Info("处理已完成的迁移", "migrationID", migrationID, "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
	// TODO: 清理迁移标签和注解
	return nil
}

func (c *ReschedulerController) handleFailedMigration(_ context.Context, pod *v1.Pod, migrationID string) error {
	c.logger.V(4).Info("处理失败的迁移", "migrationID", migrationID, "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
	// TODO: 实现失败处理逻辑，如重试或清理
	return nil
}

// Stop 停止控制器
func (c *ReschedulerController) Stop() {
	close(c.stopCh)
}
