# Rescheduler 调度性能测试用例

本测试用例旨在评估Kubernetes Rescheduler在大规模Pod调度场景下的性能和负载均衡效果。

## 测试概述

- **测试规模**: 500+ Pods
- **工作负载类型**: 4种不同特征的应用
- **测试目标**: 观察调度均匀性、资源利用率和重调度效果

## 文件说明

### 1. `load-test-500-pods.yaml`
测试负载配置文件，包含：

- **CPU密集型应用** (150 pods): 高CPU使用率，测试CPU资源调度
- **内存密集型应用** (150 pods): 高内存使用率，测试内存资源调度  
- **均衡负载应用** (100 pods): 使用nginx，模拟Web服务负载
- **轻量级应用** (100 pods): 最小资源需求，测试基础调度

### 2. `test-runner.sh`
自动化测试脚本，功能包括：

- 自动部署测试负载
- 实时监控调度情况
- 生成详细的分析报告
- 测试环境清理

## 快速开始

### 运行完整测试
```bash
cd test-cases
./test-runner.sh
```

### 分步执行测试
```bash
# 1. 仅部署测试负载
./test-runner.sh --deploy-only

# 2. 监控调度情况（在另一个终端）
./test-runner.sh --monitor-only

# 3. 清理测试环境
./test-runner.sh --cleanup
```

## 监控要点

### 1. 调度均匀性
- 各节点Pod分布是否均匀
- 是否存在"热点"节点
- Pod分布方差和标准差

### 2. 资源利用率
- CPU/内存使用率分布
- 资源请求vs实际使用
- 节点资源碎片化程度

### 3. 调度延迟
- Pod从Pending到Running的时间
- 大批量Pod调度的并发性能
- Rescheduler的响应时间

### 4. 重调度效果
- Rescheduler是否检测到不均衡
- 重调度动作的频率和效果
- 调度策略的优化程度

## 预期结果

### 理想情况
- **Pod分布**: 各节点Pod数量差异 ≤ 10%
- **调度成功率**: ≥ 95%
- **资源利用率**: 各节点CPU/内存使用均匀
- **重调度**: 能检测并修复不均衡情况

### 评估指标
1. **负载均衡评分**:
   - 优秀: Pod分布方差 < 10
   - 良好: Pod分布方差 < 25  
   - 一般: Pod分布方差 < 50
   - 需要改进: Pod分布方差 ≥ 50

2. **调度效率**:
   - 所有Pod在5分钟内完成调度
   - 失败/重启Pod数量 < 5%

3. **资源均衡性**:
   - 节点间CPU使用率差异 < 20%
   - 节点间内存使用率差异 < 20%

## 故障排查

### 常见问题

1. **Pod调度失败**
   ```bash
   kubectl describe pod <pod-name> -n load-test
   kubectl get events -n load-test --sort-by='.lastTimestamp'
   ```

2. **节点资源不足**
   ```bash
   kubectl describe nodes
   kubectl top nodes
   ```

3. **Rescheduler未工作**
   ```bash
   kubectl logs -n kube-system deployment/rescheduler-scheduler
   kubectl get pods -n kube-system | grep rescheduler
   ```

### 调试命令

```bash
# 查看Pod分布
kubectl get pods -n load-test -o wide

# 查看资源使用
kubectl top nodes
kubectl top pods -n load-test

# 查看调度事件
kubectl get events -n load-test --sort-by='.lastTimestamp'

# 查看Rescheduler日志
kubectl logs -f -n kube-system deployment/rescheduler-scheduler
```

## 扩展测试

### 增加测试规模
修改 `load-test-500-pods.yaml` 中的 `replicas` 值：
```yaml
spec:
  replicas: 200  # 增加到200个副本
```

### 添加资源压力
增加资源请求限制：
```yaml
resources:
  requests:
    cpu: "200m"      # 增加CPU请求
    memory: "256Mi"  # 增加内存请求
```

### 测试不同调度策略
修改Pod的nodeSelector或添加亲和性规则：
```yaml
spec:
  affinity:
    nodeAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        preference:
          matchExpressions:
          - key: node-size
            operator: In
            values: ["large"]
```

## 结果分析

测试完成后，查看 `results/` 目录下的报告文件：

- `baseline_*.txt`: 测试前的基准数据
- `scheduling_monitor_*.txt`: 调度过程监控数据  
- `scheduling_report_*.txt`: 综合分析报告

这些报告将帮助您评估Rescheduler的性能和调度效果，并为优化调度策略提供数据支持。
