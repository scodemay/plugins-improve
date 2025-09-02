# 🔧 重调度器故障排除指南

## 📋 概述

本文档提供重调度器插件的常见问题诊断和解决方案，帮助您快速定位和解决部署、配置、运行中的问题。

## 🚨 常见问题分类

### 1. 部署相关问题
### 2. 配置相关问题  
### 3. 运行时问题
### 4. 性能问题
### 5. 重调度行为问题

---

## 🚀 部署相关问题

### ❌ 问题1：调度器Pod启动失败

**症状**：
```bash
kubectl get pods -n kube-system -l app=rescheduler-scheduler
# Pod处于Pending、CrashLoopBackOff或Error状态
```

**诊断步骤**：
```bash
# 1. 查看Pod详细状态
kubectl describe pod -n kube-system -l app=rescheduler-scheduler

# 2. 查看Pod事件
kubectl get events -n kube-system --sort-by='.lastTimestamp' | grep rescheduler

# 3. 查看容器日志
kubectl logs -n kube-system -l app=rescheduler-scheduler --previous
```

**常见原因和解决方案**：

#### 原因1：镜像拉取失败
```bash
# 问题：镜像不存在或无法访问
# 解决：检查镜像名称和拉取策略
kubectl patch deployment -n kube-system rescheduler-scheduler \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-scheduler","imagePullPolicy":"Never"}]}}}}'
```

#### 原因2：资源不足
```bash
# 问题：节点资源不足无法调度
# 解决：检查节点资源并调整资源请求
kubectl top nodes
kubectl describe node <control-plane-node>

# 降低资源请求
kubectl patch deployment -n kube-system rescheduler-scheduler \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-scheduler","resources":{"requests":{"cpu":"50m","memory":"64Mi"}}}]}}}}'
```

#### 原因3：节点选择器问题
```bash
# 问题：没有满足nodeSelector的节点
# 解决：检查节点标签
kubectl get nodes --show-labels | grep control-plane

# 如果没有control-plane标签，移除nodeSelector
kubectl patch deployment -n kube-system rescheduler-scheduler \
  --type=json -p='[{"op": "remove", "path": "/spec/template/spec/nodeSelector"}]'
```

### ❌ 问题2：RBAC权限不足

**症状**：
```bash
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "forbidden"
# 出现权限拒绝错误
```

**诊断步骤**：
```bash
# 检查ServiceAccount
kubectl get serviceaccount -n kube-system rescheduler-scheduler

# 检查ClusterRoleBinding
kubectl get clusterrolebinding rescheduler-scheduler

# 验证具体权限
kubectl auth can-i create pods --as=system:serviceaccount:kube-system:rescheduler-scheduler
kubectl auth can-i create pods/eviction --as=system:serviceaccount:kube-system:rescheduler-scheduler
kubectl auth can-i update deployments --as=system:serviceaccount:kube-system:rescheduler-scheduler
```

**解决方案**：
```bash
# 重新应用RBAC配置
kubectl apply -f manifests/rescheduler/rbac.yaml

# 验证权限修复
kubectl auth can-i "*" "*" --as=system:serviceaccount:kube-system:rescheduler-scheduler
```

---

## ⚙️ 配置相关问题

### ❌ 问题3：配置文件格式错误

**症状**：
```bash
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "unmarshal\|parse"
# 配置解析错误
```

**诊断步骤**：
```bash
# 1. 验证YAML格式
kubectl get configmap -n kube-system rescheduler-config -o yaml

# 2. 验证配置结构
yamllint <config-file>

# 3. 测试配置加载
kubectl create configmap test-config --from-file=config.yaml --dry-run=client
```

**解决方案**：
```bash
# 使用已知正确的配置模板
kubectl apply -f manifests/rescheduler/config.yaml

# 或手动修复配置
kubectl edit configmap -n kube-system rescheduler-config
```

### ❌ 问题4：插件未正确注册

**症状**：
```bash
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "plugin.*not found"
# 插件注册失败
```

**诊断步骤**：
```bash
# 检查插件配置
kubectl get configmap -n kube-system rescheduler-config -o jsonpath='{.data.config\.yaml}' | grep -A 10 "plugins:"

# 检查调度器版本
kubectl logs -n kube-system -l app=rescheduler-scheduler | head -10
```

**解决方案**：
```bash
# 确保插件正确配置在所需的扩展点
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "apiVersion: kubescheduler.config.k8s.io/v1\nkind: KubeSchedulerConfiguration\nprofiles:\n- schedulerName: rescheduler-scheduler\n  plugins:\n    filter:\n      enabled: [{name: Rescheduler}]\n    score:\n      enabled: [{name: Rescheduler}]\n    preBind:\n      enabled: [{name: Rescheduler}]"
  }
}'
```

---

## 🔄 运行时问题

### ❌ 问题5：重调度器不工作

**症状**：
- 重调度器启动正常但不执行重调度操作
- 日志中没有重调度相关信息

**诊断步骤**：
```bash
# 1. 检查重调度器是否启动
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "重调度器开始运行"

# 2. 检查配置是否正确加载
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "配置已加载"

# 3. 检查是否达到重调度条件
kubectl top nodes
kubectl get pods --all-namespaces -o wide | awk '{print $8}' | sort | uniq -c

# 4. 检查是否有可重调度的Pod
kubectl get pods --all-namespaces --field-selector=status.phase=Running -o wide
```

**可能原因和解决方案**：

#### 原因1：重调度间隔设置为0
```bash
# 检查间隔配置
kubectl get configmap -n kube-system rescheduler-config -o jsonpath='{.data.config\.yaml}' | grep reschedulingInterval

# 修复：设置合理的间隔
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "<current-config-with-interval-30s>"
  }
}'
```

#### 原因2：没有启用重调度策略
```bash
# 检查策略配置
kubectl get configmap -n kube-system rescheduler-config -o jsonpath='{.data.config\.yaml}' | grep -A 5 enabledStrategies

# 修复：启用至少一个策略
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "<config-with-enabled-strategies>"
  }
}'
```

#### 原因3：阈值设置过高
```bash
# 检查当前阈值和实际使用率
kubectl get configmap -n kube-system rescheduler-config -o jsonpath='{.data.config\.yaml}' | grep -E "(cpu|memory)Threshold"
kubectl top nodes

# 如果使用率低于阈值，降低阈值进行测试
```

### ❌ 问题6：Pod重调度失败

**症状**：
```bash
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "重调度失败\|migration failed"
```

**诊断步骤**：
```bash
# 1. 检查失败原因
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep -A 5 -B 5 "失败"

# 2. 检查目标节点资源
kubectl describe node <target-node>

# 3. 检查Pod约束条件
kubectl describe pod <failing-pod>

# 4. 检查驱逐权限
kubectl auth can-i create pods/eviction --as=system:serviceaccount:kube-system:rescheduler-scheduler
```

**常见解决方案**：

#### 驱逐权限不足
```bash
# 添加驱逐权限到ClusterRole
kubectl patch clusterrole rescheduler-scheduler --type=json -p='
[
  {
    "op": "add",
    "path": "/rules/-",
    "value": {
      "apiGroups": [""],
      "resources": ["pods/eviction"],
      "verbs": ["create"]
    }
  }
]'
```

#### 目标节点资源不足
```bash
# 检查节点可用资源
kubectl describe node <target-node> | grep -A 5 "Allocated resources"

# 检查Pod资源请求
kubectl get pod <pod-name> -o jsonpath='{.spec.containers[*].resources}'
```

---

## 📊 性能问题

### ❌ 问题7：调度器性能慢

**症状**：
- Pod调度延迟明显增加
- 调度器CPU/内存使用率过高

**诊断步骤**：
```bash
# 1. 检查调度器资源使用
kubectl top pod -n kube-system -l app=rescheduler-scheduler

# 2. 检查调度延迟
kubectl get events --sort-by='.lastTimestamp' | grep Scheduled

# 3. 检查日志中的性能指标
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep -E "(耗时|duration|took)"
```

**优化方案**：

#### 调整资源限制
```bash
# 增加调度器资源限制
kubectl patch deployment -n kube-system rescheduler-scheduler -p='
{
  "spec": {
    "template": {
      "spec": {
        "containers": [
          {
            "name": "kube-scheduler",
            "resources": {
              "limits": {
                "cpu": "1000m",
                "memory": "1Gi"
              },
              "requests": {
                "cpu": "200m",
                "memory": "256Mi"
              }
            }
          }
        ]
      }
    }
  }
}'
```

#### 调整QPS限制
```bash
# 增加API客户端QPS
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "<config-with-higher-qps>"
  }
}'
```

#### 降低重调度频率
```bash
# 增加重调度间隔
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "<config-with-longer-interval>"
  }
}'
```

### ❌ 问题8：过度重调度

**症状**：
- 重调度过于频繁
- Pod频繁在节点间迁移
- 应用服务不稳定

**诊断步骤**：
```bash
# 1. 统计重调度频率
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "重调度" | wc -l

# 2. 检查重调度原因
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep -A 2 "开始执行Pod迁移"

# 3. 查看集群负载波动
kubectl top nodes --sort-by=cpu
```

**解决方案**：

#### 调整阈值
```bash
# 提高重调度阈值
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "<config-with-higher-thresholds>"
  }
}'
```

#### 限制重调度数量
```bash
# 降低最大重调度Pod数量
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "<config-with-lower-max-pods>"
  }
}'
```

#### 增加排除规则
```bash
# 排除更多命名空间或添加排除标签
kubectl patch configmap -n kube-system rescheduler-config --type=merge -p='
{
  "data": {
    "config.yaml": "<config-with-more-exclusions>"
  }
}'
```

---

## 🎯 调试技巧

### 启用详细日志
```bash
# 增加日志级别
kubectl patch deployment -n kube-system rescheduler-scheduler -p='
{
  "spec": {
    "template": {
      "spec": {
        "containers": [
          {
            "name": "kube-scheduler",
            "args": [
              "--config=/etc/kubernetes/config.yaml",
              "--v=4"
            ]
          }
        ]
      }
    }
  }
}'
```

### 实时监控
```bash
# 实时查看重调度行为
kubectl logs -n kube-system -l app=rescheduler-scheduler -f | grep -E "(重调度|migration|scheduling)"

# 监控Pod分布
watch "kubectl get pods --all-namespaces -o wide | awk '{print \$8}' | sort | uniq -c"

# 监控节点资源
watch kubectl top nodes
```

### 测试配置
```bash
# 创建测试配置
kubectl create configmap rescheduler-config-test --from-file=test-config.yaml --dry-run=client -o yaml

# 应用测试配置
kubectl patch configmap -n kube-system rescheduler-config --patch-file test-config.yaml

# 观察行为变化
kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=100 -f
```

## 🔍 常用诊断命令

### 系统状态检查
```bash
# 完整系统检查脚本
#!/bin/bash
echo "=== 重调度器状态检查 ==="

echo "1. Pod状态:"
kubectl get pods -n kube-system -l app=rescheduler-scheduler

echo "2. 配置状态:"
kubectl get configmap -n kube-system rescheduler-config

echo "3. 服务状态:"
kubectl get service -n kube-system rescheduler-scheduler-metrics

echo "4. 最近日志:"
kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=20

echo "5. 节点状态:"
kubectl get nodes -o wide

echo "6. Pod分布:"
kubectl get pods --all-namespaces -o wide | awk '{print $8}' | sort | uniq -c
```

### 性能监控
```bash
# 性能监控脚本
#!/bin/bash
while true; do
  echo "=== $(date) ==="
  echo "调度器资源使用:"
  kubectl top pod -n kube-system -l app=rescheduler-scheduler
  
  echo "节点资源使用:"
  kubectl top nodes
  
  echo "最近重调度:"
  kubectl logs -n kube-system -l app=rescheduler-scheduler --since=60s | grep "重调度" | wc -l
  
  echo "---"
  sleep 60
done
```

## 📞 获取帮助

### 收集诊断信息
在寻求帮助时，请收集以下信息：

```bash
# 创建诊断信息包
mkdir rescheduler-debug
cd rescheduler-debug

# 收集基本信息
kubectl version > k8s-version.txt
kubectl get nodes -o wide > nodes.txt
kubectl get pods -n kube-system -l app=rescheduler-scheduler -o yaml > scheduler-pods.yaml
kubectl get configmap -n kube-system rescheduler-config -o yaml > config.yaml
kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=1000 > scheduler-logs.txt
kubectl get events -n kube-system --sort-by='.lastTimestamp' > events.txt

# 压缩信息包
cd ..
tar -czf rescheduler-debug.tar.gz rescheduler-debug/
```

### 社区支持
- 📧 GitHub Issues: [scheduler-plugins/issues](https://github.com/scheduler-plugins/issues)
- 📖 文档: [重调度器文档](./README.md)
- 🔧 配置参考: [配置指南](./configuration.md)

---

**相关文档**: [部署指南](./deployment-guide.md) | [配置参考](./configuration.md) | [使用示例](./examples.md)
