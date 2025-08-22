# Rescheduler Plugin - 重调度插件

## 概述

Rescheduler插件是一个智能的Kubernetes Pod重调度解决方案，能够持续监控集群状态并根据多种策略自动重新调度Pod，以优化资源利用率和集群性能。

## 功能特性

### 🚀 多种重调度策略

1. **负载均衡 (LoadBalancing)**
   - 监控节点间的负载分布
   - 自动将Pod从高负载节点迁移到低负载节点
   - 维持集群整体负载平衡

2. **资源优化 (ResourceOptimization)**
   - 监控节点CPU和内存使用率
   - 当节点资源使用超过阈值时触发重调度
   - 优化整体资源利用效率

3. **节点维护 (NodeMaintenance)**
   - 支持节点维护模式
   - 自动迁移维护节点上的所有Pod
   - 确保服务不中断

### 🛡️ 安全性保障

- **智能Pod筛选**: 自动排除系统Pod、DaemonSet Pod和静态Pod
- **命名空间隔离**: 支持排除指定命名空间
- **优先级感知**: 优先迁移低优先级Pod
- **标签控制**: 支持通过标签控制Pod是否参与重调度

### ⚙️ 可配置参数

- 重调度间隔时间
- 资源使用率阈值
- 负载不均衡阈值
- 最大重调度Pod数量
- 排除规则

## 使用方法

### 1. 配置调度器

在调度器配置文件中启用Rescheduler插件：

```yaml
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
leaderElection:
  leaderElect: false
clientConnection:
  kubeconfig: "/path/to/your/kubeconfig"
profiles:
  - schedulerName: rescheduler-scheduler
    plugins:
      # Rescheduler插件会在后台运行，不需要特定的扩展点
    pluginConfig:
      - name: Rescheduler
        args:
          reschedulingInterval: "30s"
          enabledStrategies:
            - "LoadBalancing"
            - "ResourceOptimization"
            - "NodeMaintenance"
          cpuThreshold: 80.0
          memoryThreshold: 80.0
          imbalanceThreshold: 20.0
          maxReschedulePods: 10
          excludedNamespaces:
            - "kube-system"
            - "kube-public"
```

### 2. 启动调度器

```bash
# 编译调度器
make build

# 运行调度器
./bin/kube-scheduler --config=manifests/rescheduler/scheduler-config.yaml --v=2
```

### 3. 控制Pod重调度行为

#### 排除Pod参与重调度

为Pod添加标签来排除重调度：

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
  labels:
    scheduler.alpha.kubernetes.io/rescheduling: "disabled"
spec:
  # ... pod spec
```

#### 节点维护模式

为节点添加标签启用维护模式：

```bash
kubectl label node worker-1 scheduler.alpha.kubernetes.io/maintenance=true
```

## 工作原理

### 监控和分析
1. **持续监控**: 定期收集所有节点和Pod的状态信息
2. **资源计算**: 计算每个节点的CPU、内存使用率和Pod分布
3. **策略评估**: 根据配置的策略评估是否需要重调度

### 决策制定
1. **负载分析**: 识别高负载和低负载节点
2. **Pod筛选**: 选择符合条件的可迁移Pod
3. **目标选择**: 为每个Pod选择最优的目标节点

### 安全迁移
1. **创建副本**: 在目标节点创建Pod副本
2. **状态验证**: 等待新Pod正常运行
3. **优雅删除**: 删除原始Pod

## 监控和日志

### 日志示例

```
I1201 10:30:15.123456 1 rescheduler.go:120] 重调度器开始运行 interval=30s
I1201 10:30:45.234567 1 rescheduler.go:145] 开始执行重调度检查
I1201 10:30:45.345678 1 rescheduler.go:380] 开始执行Pod迁移 pod=default/nginx-abc123 sourceNode=worker-1 targetNode=worker-2 reason="负载均衡: 源节点使用率85.0%, 目标节点使用率45.0%" strategy=LoadBalancing
I1201 10:30:45.456789 1 rescheduler.go:425] 成功创建迁移Pod newPod=default/nginx-abc123-migrated-1701421845
I1201 10:31:15.567890 1 rescheduler.go:445] 成功删除原Pod pod=default/nginx-abc123
I1201 10:30:45.678901 1 rescheduler.go:185] 完成重调度操作 重调度Pod数量=3
```

### 迁移Pod标识

迁移后的Pod会包含以下标签和注解：

**标签:**
- `scheduler.alpha.kubernetes.io/migrated-from`: 原始节点名称
- `scheduler.alpha.kubernetes.io/migration-reason`: 迁移策略

**注解:**
- `scheduler.alpha.kubernetes.io/migration-time`: 迁移时间
- `scheduler.alpha.kubernetes.io/original-pod`: 原始Pod UID

## 配置参数详解

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `reschedulingInterval` | duration | 30s | 重调度检查间隔 |
| `enabledStrategies` | []string | ["LoadBalancing", "ResourceOptimization"] | 启用的重调度策略 |
| `cpuThreshold` | float64 | 80.0 | CPU使用率阈值(%) |
| `memoryThreshold` | float64 | 80.0 | 内存使用率阈值(%) |
| `imbalanceThreshold` | float64 | 20.0 | 负载不均衡阈值(%) |
| `maxReschedulePods` | int | 10 | 单次重调度最大Pod数量 |
| `excludedNamespaces` | []string | ["kube-system", "kube-public"] | 排除的命名空间 |
| `excludedPodSelector` | string | "" | 排除的Pod标签选择器 |

## 最佳实践

### 1. 配置建议
- 根据集群规模调整重调度间隔
- 设置合理的资源阈值，避免频繁迁移
- 限制单次重调度Pod数量，确保集群稳定

### 2. 监控建议
- 监控重调度频率和成功率
- 观察集群负载分布变化
- 关注Pod迁移对应用的影响

### 3. 故障排除
- 检查调度器日志获取详细信息
- 验证节点资源状态
- 确认Pod和节点标签配置

## 注意事项

### ⚠️ 重要提醒

1. **有状态应用**: 谨慎对有状态应用使用重调度，可能导致数据丢失
2. **网络依赖**: 考虑Pod迁移对网络连接的影响
3. **资源限制**: 确保目标节点有足够资源运行迁移的Pod
4. **测试环境**: 建议先在测试环境验证重调度策略

### 🔒 安全考虑

- 重调度器需要集群级别的Pod读写权限
- 建议使用RBAC限制权限范围
- 监控重调度操作的审计日志

## 发展路线

- [ ] 支持更多重调度策略（拓扑感知、成本优化等）
- [ ] 实现更精确的资源使用率计算
- [ ] 支持Pod组级别的重调度
- [ ] 集成Prometheus指标
- [ ] Web UI管理界面

## 贡献

欢迎贡献代码、报告问题或提出改进建议！

## 许可证

本项目采用 Apache 2.0 许可证。
