# Rescheduler插件单独部署指南

## 概述

Rescheduler是Scheduler-Plugins项目中的核心插件，专门用于集群负载均衡和工作负载重新调度。本指南详细说明如何单独部署和配置Rescheduler插件。

## 插件特性

### 核心功能
- **智能负载均衡**: 基于实时资源使用率进行Pod重新分配
- **预防性重调度**: 在资源不足前主动迁移工作负载
- **多资源评分**: 综合CPU、内存等资源进行调度决策
- **可配置阈值**: 灵活的触发条件和参数调整

### 工作模式
1. **调度模式**: 优化新Pod的初始放置
2. **重调度模式**: 持续监控并重新平衡现有工作负载

## 部署步骤

### 第一步: 部署前置资源

#### 1.1 创建优先级类
```bash
kubectl apply -f manifests/rescheduler/priority-class.yaml
```

**文件内容解析**:
```yaml
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: rescheduler-priority
value: 1000000              # 高优先级，确保调度器稳定运行
globalDefault: false
description: "Priority class for rescheduler scheduler"
```

#### 1.2 创建RBAC权限
```bash
kubectl apply -f manifests/rescheduler/rbac.yaml
```

**关键权限说明**:
- `pods`: 管理Pod的生命周期
- `nodes`: 获取节点信息和状态
- `pods/binding`: 执行Pod绑定操作
- `pods/eviction`: 执行Pod驱逐操作
- `events`: 记录调度事件
- `configmaps`: 读取配置信息

### 第二步: 配置调度器

#### 2.1 应用配置映射
```bash
kubectl apply -f manifests/rescheduler/configmap.yaml
```

#### 2.2 自定义配置参数

编辑ConfigMap以调整调度行为:

```bash
kubectl edit configmap rescheduler-config -n kube-system
```

**核心配置参数详解**:

```yaml
data:
  config.yaml: |
    apiVersion: kubescheduler.config.k8s.io/v1
    kind: KubeSchedulerConfiguration
    profiles:
    - schedulerName: rescheduler-scheduler
      plugins:
        filter:
          enabled:
          - name: Rescheduler
        score:
          enabled:
          - name: Rescheduler
        reserve:
          enabled:
          - name: Rescheduler
        preBind:
          enabled:
          - name: Rescheduler
      pluginConfig:
      - name: Rescheduler
        args:
          # === 资源阈值配置 ===
          cpuThreshold: 80.0              # CPU使用率阈值(%)
          memoryThreshold: 80.0           # 内存使用率阈值(%)
          
          # === 调度优化开关 ===
          enableSchedulingOptimization: true      # 启用调度优化
          enablePreventiveRescheduling: true      # 启用预防性重调度
          
          # === 评分权重配置 ===
          cpuScoreWeight: 0.6             # CPU权重 (0.0-1.0)
          memoryScoreWeight: 0.4          # 内存权重 (0.0-1.0)
          loadBalanceBonus: 10.0          # 负载均衡奖励分数
          
          # === 重调度控制器 ===
          enableReschedulingController: true      # 启用重调度控制器
          reschedulingInterval: "30s"             # 重调度检查间隔
          
          # === 排除命名空间 ===
          excludedNamespaces:
          - kube-system                   # 系统命名空间
          - kube-public                   # 公共命名空间
          - monitoring                    # 监控命名空间(可选)
```

### 第三步: 部署调度器

#### 3.1 部署Rescheduler调度器
要先构建调度器镜像，之后再使用依赖文件部署
```bash
kubectl apply -f manifests/rescheduler/deployment.yaml
```



### 第四步: 验证部署

#### 4.1 检查调度器状态
```bash
# 查看Pod状态
kubectl get pods -n kube-system -l app=rescheduler-scheduler

# 预期输出:
# NAME                                   READY   STATUS    RESTARTS   AGE
# rescheduler-scheduler-xxxx-xxxx        1/1     Running   0          2m
```

#### 4.2 检查调度器日志
```bash
kubectl logs -n kube-system -l app=rescheduler-scheduler -f

# 正常启动日志示例:
# I0101 12:00:00.123456       1 rescheduler.go:125] "Rescheduler plugin initialized successfully"
# I0101 12:00:00.123456       1 rescheduler.go:458] "Starting rescheduling controller with interval 30s"
# I0101 12:00:00.123456       1 server.go:142] "Starting Kubernetes Scheduler" version="v1.28.0"
```

#### 4.3 验证调度器注册
```bash
# 检查调度器是否正确注册
kubectl get events -n kube-system | grep rescheduler-scheduler

# 或者查看调度器Leader选举状态
kubectl get leases -n kube-system | grep rescheduler
```

## 使用Rescheduler调度器

### 基本使用

在工作负载中指定schedulerName:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-application
spec:
  replicas: 10
  selector:
    matchLabels:
      app: my-application
  template:
    metadata:
      labels:
        app: my-application
    spec:
      schedulerName: rescheduler-scheduler  # 指定使用Rescheduler调度器
      containers:
      - name: app-container
        image: nginx:latest
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
```

### 验证调度效果

```bash
# 查看Pod分布
kubectl get pods -o wide -l app=my-application

# 观察重调度活动
kubectl get events --field-selector reason=Rescheduled

# 查看负载均衡效果
kubectl top nodes
```

## 配置调优

### 生产环境推荐配置

```yaml
pluginConfig:
- name: Rescheduler
  args:
    # 保守的阈值设置
    cpuThreshold: 85.0
    memoryThreshold: 85.0
    
    # 较长的检查间隔
    reschedulingInterval: "60s"
    
    # 关闭预防性重调度(用于关键业务)
    enablePreventiveRescheduling: false
    
    # 更多排除命名空间
    excludedNamespaces:
    - kube-system
    - kube-public
    - monitoring
    - logging
    - critical-apps
```

### 高频重调度配置

```yaml
pluginConfig:
- name: Rescheduler
  args:
    # 更激进的阈值
    cpuThreshold: 70.0
    memoryThreshold: 75.0
    
    # 更频繁的检查
    reschedulingInterval: "15s"
    
    # 启用预防性重调度
    enablePreventiveRescheduling: true
    
    # 调整评分权重
    cpuScoreWeight: 0.7
    memoryScoreWeight: 0.3
    loadBalanceBonus: 15.0
```

### 节点特定配置

如果需要针对特定节点类型进行调度优化:

```yaml
# 在deployment.yaml中添加节点选择器
spec:
  template:
    spec:
      nodeSelector:
        node-role.kubernetes.io/control-plane: ""  # 仅在控制平面节点运行
      
      # 或使用亲和性规则
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: node-role.kubernetes.io/control-plane
                operator: Exists
```

## 监控和观察

### 启用调度器指标

调度器自动暴露Prometheus指标，端口10259:

```bash
# 检查指标端点
kubectl port-forward -n kube-system svc/rescheduler-scheduler 10259:10259 &
curl http://localhost:10259/metrics | grep scheduler_plugin
```

### 关键指标

- `scheduler_plugin_execution_duration_seconds{plugin="Rescheduler"}`: 插件执行时间
- `rescheduler_pod_movements_total`: Pod重调度次数
- `rescheduler_load_balance_score`: 集群负载均衡分数
- `rescheduler_node_utilization_percent`: 节点资源利用率

### 部署监控系统

```bash
# 部署完整监控栈
./tools/monitoring/deploy-enhanced-monitoring.sh

# 访问Grafana
kubectl port-forward -n monitoring svc/grafana-service 3000:3000 &
# 浏览器访问 http://localhost:3000 (admin/admin123)
```

## 故障排除

### 常见问题和解决方案

#### 1. 调度器Pod启动失败

**症状**: 调度器Pod处于CrashLoopBackOff状态

**诊断**:
```bash
kubectl describe pod -n kube-system -l app=rescheduler-scheduler
kubectl logs -n kube-system -l app=rescheduler-scheduler
```

**常见原因和解决方案**:
- **配置错误**: 检查ConfigMap中的YAML语法
- **RBAC权限不足**: 确保应用了完整的RBAC配置
- **镜像拉取失败**: 检查镜像名称和拉取策略

#### 2. Pod未被重调度

**症状**: 集群存在负载不均，但Pod没有被重新调度

**诊断**:
```bash
# 检查重调度控制器日志
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep -i rescheduling

# 验证Pod驱逐权限
kubectl auth can-i create pods/eviction --as=system:serviceaccount:kube-system:rescheduler-scheduler
```

**解决方案**:
```bash
# 应用额外的Pod驱逐权限
kubectl apply -f fix-rescheduler-permissions.yaml

# 重启调度器使权限生效
kubectl rollout restart deployment/rescheduler-scheduler -n kube-system
```

#### 3. 调度器资源消耗过高

**症状**: 调度器Pod内存或CPU使用率过高

**解决方案**:
```bash
# 调整资源限制
kubectl patch deployment rescheduler-scheduler -n kube-system -p '
{
  "spec": {
    "template": {
      "spec": {
        "containers": [{
          "name": "kube-scheduler",
          "resources": {
            "requests": {
              "cpu": "200m",
              "memory": "256Mi"
            },
            "limits": {
              "cpu": "1000m",
              "memory": "1Gi"
            }
          }
        }]
      }
    }
  }
}'
```

#### 4. 启用调试模式

```bash
# 增加日志详细级别
kubectl patch deployment rescheduler-scheduler -n kube-system -p '
{
  "spec": {
    "template": {
      "spec": {
        "containers": [{
          "name": "kube-scheduler",
          "args": [
            "--config=/etc/kubernetes/config.yaml",
            "--v=4",
            "--logtostderr=true"
          ]
        }]
      }
    }
  }
}'
```

## 性能测试

### 测试Rescheduler效果

```bash
# 创建不均衡的工作负载
kubectl apply -f test-cases/imbalance-test.yaml

# 运行性能测试
./scripts/run-performance-tests.sh

# 观察重调度过程
kubectl get events --watch | grep -i rescheduled
```

### 负载测试

```bash
# 创建高负载场景
kubectl apply -f test-cases/resource-pressure-test.yaml

# 监控调度器性能
./scripts/monitor-performance.sh 300  # 监控5分钟
```

## 卸载

### 完全移除Rescheduler

```bash
# 删除调度器部署
kubectl delete -f manifests/rescheduler/deployment.yaml

# 删除配置
kubectl delete -f manifests/rescheduler/configmap.yaml

# 删除RBAC
kubectl delete -f manifests/rescheduler/rbac.yaml

# 删除优先级类
kubectl delete -f manifests/rescheduler/priority-class.yaml

# 清理额外权限(如果之前应用过)
kubectl delete -f fix-rescheduler-permissions.yaml
```

### 迁移工作负载回默认调度器

```bash
# 将现有工作负载迁移回默认调度器
kubectl patch deployment my-application -p '
{
  "spec": {
    "template": {
      "spec": {
        "schedulerName": "default-scheduler"
      }
    }
  }
}'
```

## 最佳实践

### 生产环境建议

1. **渐进式部署**: 先在测试环境验证配置
2. **监控设置**: 部署监控系统观察调度效果
3. **资源限制**: 为调度器设置合适的资源限制
4. **命名空间排除**: 排除关键系统命名空间
5. **备份配置**: 保存工作配置的备份

### 配置建议

- **保守阈值**: 生产环境使用较高的阈值(80-85%)
- **较长间隔**: 使用较长的重调度间隔(60s+)
- **禁用预防性重调度**: 关键应用环境中禁用
- **合理权重**: 根据集群特点调整CPU/内存权重

---

通过本指南，您应该能够成功部署和配置Rescheduler插件，实现集群的智能负载均衡。如遇问题，请参考故障排除章节或查看调度器日志进行诊断。
