# ⚙️ 重调度器配置参考

## 📋 配置概述

重调度器插件通过 `KubeSchedulerConfiguration` 进行配置，支持丰富的参数来控制调度和重调度行为。

## 🔧 完整配置示例

### 基础配置结构
```yaml
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration

profiles:
  - schedulerName: rescheduler-scheduler
    plugins:
      filter:
        enabled: [name: Rescheduler]    # 节点过滤
      score:
        enabled: [name: Rescheduler]     # 节点打分  
      preBind:
        enabled: [name: Rescheduler]     # 预防性重调度
    
    pluginConfig:
      - name: Rescheduler
        args:
          # 配置参数详见下文
```

## 📊 核心配置参数

### 重调度基础配置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `reschedulingInterval` | duration | `"30s"` | 重调度检查间隔时间 |
| `enabledStrategies` | []string | `["LoadBalancing"]` | 启用的重调度策略列表 |
| `maxReschedulePods` | int | `10` | 单次重调度的最大Pod数量 |

#### 重调度策略列表
- **`LoadBalancing`**: 负载均衡策略，平衡节点间Pod分布
- **`ResourceOptimization`**: 资源优化策略，基于CPU/内存使用率
- **`NodeMaintenance`**: 节点维护策略，支持节点维护模式

### 资源阈值配置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `cpuThreshold` | float64 | `80.0` | CPU使用率阈值（百分比，0-100） |
| `memoryThreshold` | float64 | `80.0` | 内存使用率阈值（百分比，0-100） |
| `imbalanceThreshold` | float64 | `20.0` | 负载不均衡阈值（百分比） |

### 排除配置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `excludedNamespaces` | []string | `["kube-system", "kube-public"]` | 排除的命名空间列表 |
| `excludedPodSelector` | string | `""` | 排除Pod的标签选择器 |

### 调度优化配置

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `enableSchedulingOptimization` | bool | `true` | 是否启用调度优化（Filter+Score） |
| `enablePreventiveRescheduling` | bool | `true` | 是否启用预防性重调度（PreBind） |
| `cpuScoreWeight` | float64 | `0.6` | CPU在Score计算中的权重（0.0-1.0） |
| `memoryScoreWeight` | float64 | `0.4` | 内存在Score计算中的权重（0.0-1.0） |
| `loadBalanceBonus` | float64 | `10.0` | 负载均衡奖励分数（0-50） |

## 🎯 配置场景与建议

### 生产环境（保守配置）
```yaml
pluginConfig:
- name: Rescheduler
  args:
    reschedulingInterval: "120s"              # 降低检查频率
    enabledStrategies: ["LoadBalancing"]      # 仅启用负载均衡
    cpuThreshold: 95.0                        # 提高阈值
    memoryThreshold: 95.0
    imbalanceThreshold: 40.0
    maxReschedulePods: 3                      # 限制重调度数量
    
    excludedNamespaces:
      - "kube-system"
      - "kube-public"
      - "istio-system"
      - "monitoring"
      - "database"
    
    enableSchedulingOptimization: true
    enablePreventiveRescheduling: false       # 关闭预防性重调度
    loadBalanceBonus: 3.0                     # 小的奖励分数
```

### 开发测试环境（激进配置）
```yaml
pluginConfig:
- name: Rescheduler
  args:
    reschedulingInterval: "15s"               # 高频检查
    enabledStrategies:
      - "LoadBalancing"
      - "ResourceOptimization"
      - "NodeMaintenance"
    cpuThreshold: 50.0                        # 低阈值
    memoryThreshold: 50.0
    imbalanceThreshold: 10.0
    maxReschedulePods: 50                     # 高重调度限制
    
    excludedNamespaces:
      - "kube-system"
    
    enableSchedulingOptimization: true
    enablePreventiveRescheduling: true        # 启用所有功能
    loadBalanceBonus: 20.0                    # 高奖励分数
```

### CPU密集型环境（HPC配置）
```yaml
pluginConfig:
- name: Rescheduler
  args:
    reschedulingInterval: "60s"
    enabledStrategies: ["LoadBalancing", "ResourceOptimization"]
    cpuThreshold: 85.0                        # CPU重要，阈值稍低
    memoryThreshold: 95.0                     # 内存次要，阈值较高
    imbalanceThreshold: 15.0
    
    enableSchedulingOptimization: true
    enablePreventiveRescheduling: true
    cpuScoreWeight: 0.8                       # 重视CPU
    memoryScoreWeight: 0.2
    loadBalanceBonus: 15.0
```

### 内存密集型环境配置
```yaml
pluginConfig:
- name: Rescheduler
  args:
    reschedulingInterval: "45s"
    enabledStrategies: ["LoadBalancing", "ResourceOptimization"]
    cpuThreshold: 90.0                        # CPU次要
    memoryThreshold: 75.0                     # 内存重要，阈值较低
    
    enableSchedulingOptimization: true
    enablePreventiveRescheduling: true
    cpuScoreWeight: 0.3                       # 轻视CPU
    memoryScoreWeight: 0.7                    # 重视内存
    loadBalanceBonus: 12.0
```

## 🔍 高级配置选项

### 领导者选举配置
```yaml
leaderElection:
  leaderElect: true
  leaseDuration: 15s      # 租约持续时间
  renewDeadline: 10s      # 续约截止时间
  retryPeriod: 2s         # 重试间隔
  resourceLock: leases    # 锁资源类型
  resourceNamespace: kube-system
  resourceName: rescheduler-scheduler
```

### 客户端连接配置
```yaml
clientConnection:
  kubeconfig: ""          # 集群内使用空字符串
  qps: 100               # 每秒查询数限制
  burst: 200             # 突发查询数限制
```

### 性能调优配置
```yaml
# 生产环境性能优化
clientConnection:
  qps: 50                # 降低QPS减少API服务器压力
  burst: 100

leaderElection:
  leaseDuration: 30s     # 增加租约时间提高稳定性
  renewDeadline: 20s
  retryPeriod: 5s

# 开发环境高性能
clientConnection:
  qps: 200               # 提高QPS加快响应
  burst: 400

leaderElection:
  leaderElect: false     # 开发环境可以关闭领导者选举
```

## 📋 配置验证

### 验证配置语法
```bash
# 验证YAML语法
kubectl apply --dry-run=client -f config.yaml

# 验证配置结构
kubectl create configmap test-config --from-file=config.yaml --dry-run=client -o yaml
```

### 测试配置生效
```bash
# 应用新配置
kubectl apply -f config.yaml

# 重启调度器
kubectl rollout restart deployment -n kube-system rescheduler-scheduler

# 查看启动日志
kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=50
```

## 🎛️ 动态配置调整

### 运行时修改配置
```bash
# 编辑配置
kubectl edit configmap -n kube-system rescheduler-config

# 触发配置重载（重启Pod）
kubectl rollout restart deployment -n kube-system rescheduler-scheduler

# 验证新配置
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "配置已加载"
```

### 配置回滚
```bash
# 查看历史版本
kubectl rollout history deployment -n kube-system rescheduler-scheduler

# 回滚到上一版本
kubectl rollout undo deployment -n kube-system rescheduler-scheduler

# 回滚到指定版本
kubectl rollout undo deployment -n kube-system rescheduler-scheduler --to-revision=2
```

## 🚨 配置注意事项

### 配置限制和约束

1. **权重约束**: `cpuScoreWeight + memoryScoreWeight` 应该等于 1.0
2. **阈值范围**: 所有阈值参数应该在 0-100 之间
3. **间隔限制**: `reschedulingInterval` 最小值为 10s，建议不小于 30s
4. **数量限制**: `maxReschedulePods` 建议不超过集群Pod总数的 10%

### 性能影响考虑

1. **检查频率**: 过高的检查频率会增加API服务器负载
2. **重调度数量**: 过多的重调度可能影响集群稳定性
3. **阈值设置**: 过低的阈值可能导致频繁重调度
4. **排除配置**: 合理排除关键服务避免误操作

### 安全性考虑

1. **权限控制**: 确保调度器具有必要但不过度的权限
2. **命名空间隔离**: 排除关键系统命名空间
3. **标签控制**: 使用标签选择器精确控制重调度范围
4. **监控审计**: 监控重调度操作的审计日志

## 🔧 故障排除

### 常见配置问题

1. **配置格式错误**
   ```bash
   # 检查YAML格式
   yamllint config.yaml
   ```

2. **参数类型错误**
   ```bash
   # 查看调度器启动错误
   kubectl logs -n kube-system -l app=rescheduler-scheduler | grep ERROR
   ```

3. **权限不足**
   ```bash
   # 检查RBAC权限
   kubectl auth can-i create pods/eviction --as=system:serviceaccount:kube-system:rescheduler-scheduler
   ```

4. **配置不生效**
   ```bash
   # 确认ConfigMap更新
   kubectl get configmap -n kube-system rescheduler-config -o yaml
   
   # 确认Pod重启
   kubectl get pods -n kube-system -l app=rescheduler-scheduler
   ```

---

**相关文档**: [部署指南](./deployment-guide.md) | [使用示例](./examples.md) | [故障排除](./troubleshooting.md)
