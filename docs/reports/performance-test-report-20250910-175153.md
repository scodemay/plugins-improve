# Rescheduler 性能测试报告

**测试时间**: 2025年 09月 10日 星期三 17:51:53 CST
**测试环境**: Kubernetes 
**节点数量**: 4

## 测试摘要

### 1. 基础调度性能
- 测试用例: basic-scheduling-test.yaml
- Pod 数量: 205
- 调度成功率: 100/205

### 2. 并发调度性能
- 测试用例: concurrent-scheduling-test.yaml
- 并发度: 50 pods
- 调度分布: 
  - scheduler-stable-worker: 69 pods
  - scheduler-stable-worker2: 66 pods
  - scheduler-stable-worker3: 70 pods

### 3. 负载均衡测试
- 测试用例: imbalance-test.yaml
- 节点负载分布:
  - scheduler-stable-control-plane: CPU 2%, Memory 9%
  - scheduler-stable-worker: CPU 26%, Memory 5%
  - scheduler-stable-worker2: CPU 19%, Memory 6%
  - scheduler-stable-worker3: CPU 30%, Memory 5%

### 4. 资源压力测试
- 测试用例: resource-pressure-test.yaml
- 高资源需求 Pod 调度情况:
5 个 CPU 密集型 Pod
4 个内存密集型 Pod

## 调度器状态
- 运行状态: Running
- 资源使用: CPU: 5m, Memory: 27Mi

## 关键指标
- 总测试时间: 884 秒

### 调度成功率分析
- 服务Pod成功率: 88.57% (持续运行服务，排除Job类型)
- 任务Pod成功率: 85.00% (包含已完成的Job任务)
- 总体调度成功率: 78.05% (所有Pod，包括Running和Completed)

### 负载均衡分析
- CPU 负载标准差: 10.71%
- 内存负载标准差: 1.64%
- CPU 负载均衡度: 89.29% (良好)
- 内存负载均衡度: 98.36% (优秀)
- 综合负载均衡等级: 良好 ○

## 建议
1. 监控调度器资源使用，确保不超过集群资源的 5%
2. 观察负载均衡效果，标准差应小于 20%
3. 检查重调度频率，避免过于频繁的 Pod 迁移
4. 根据实际工作负载调整 CPU 和内存阈值配置

## 详细日志
查看调度器日志:
```bash
kubectl logs -n kube-system -l app=rescheduler-scheduler
```

查看测试 Pod 状态:
```bash
kubectl get pods -n perf-test -o wide
```

