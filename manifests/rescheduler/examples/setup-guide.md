# 重调度器测试环境搭建指南

## 🚀 第一步：搭建Kind集群

### 创建集群配置文件

```yaml
# kind-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: rescheduler-test
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 80
    hostPort: 80
    protocol: TCP
  - containerPort: 443
    hostPort: 443
    protocol: TCP
- role: worker
  labels:
    node-size: small
- role: worker
  labels:
    node-size: medium
- role: worker
  labels:
    node-size: large
```

### 创建集群

```bash
# 创建集群
kind create cluster --config kind-config.yaml

# 验证集群
kubectl get nodes -o wide
```

## 🔧 第二步：部署Metrics Server

```bash
# 部署Metrics Server（必需，控制器需要获取节点指标）
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml

# 修复Metrics Server在Kind中的TLS问题
kubectl patch -n kube-system deployment metrics-server --type='json' \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'

# 验证Metrics Server运行
kubectl top nodes
```

## 🔨 第三步：构建和部署调度器

### 构建调度器镜像

```bash
# 在scheduler-plugins项目根目录下
make build
make image

# 加载镜像到Kind集群
kind load docker-image scheduler-plugins:latest --name rescheduler-test
```

### 部署调度器

```bash
# 部署RBAC
kubectl apply -f manifests/rescheduler/rbac.yaml

# 部署配置
kubectl apply -f manifests/rescheduler/config.yaml

# 部署调度器
kubectl apply -f manifests/rescheduler/scheduler.yaml

# 验证调度器启动
kubectl get pods -n kube-system -l app=rescheduler-scheduler
kubectl logs -n kube-system -l app=rescheduler-scheduler
```

## 📊 第四步：创建测试负载

### 部署不同资源需求的工作负载

```bash
# 部署测试应用
kubectl apply -f manifests/rescheduler/examples/quick-test.yaml

# 创建高CPU需求的Pod
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cpu-intensive
  namespace: default
spec:
  replicas: 5
  selector:
    matchLabels:
      app: cpu-intensive
  template:
    metadata:
      labels:
        app: cpu-intensive
    spec:
      schedulerName: rescheduler-scheduler  # 使用重调度器
      containers:
      - name: stress
        image: polinux/stress
        args: ["stress", "--cpu", "2", "--timeout", "600s"]
        resources:
          requests:
            cpu: "500m"
            memory: "256Mi"
          limits:
            cpu: "1000m"
            memory: "512Mi"
EOF

# 创建高内存需求的Pod
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: memory-intensive
  namespace: default
spec:
  replicas: 3
  selector:
    matchLabels:
      app: memory-intensive
  template:
    metadata:
      labels:
        app: memory-intensive
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: stress
        image: polinux/stress
        args: ["stress", "--vm", "1", "--vm-bytes", "512M", "--timeout", "600s"]
        resources:
          requests:
            cpu: "100m"
            memory: "512Mi"
          limits:
            cpu: "200m"
            memory: "1Gi"
EOF
```

### 创建测试场景

```bash
# 场景1：负载不均衡测试
# 创建NodeAffinity让Pod调度到特定节点
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: unbalanced-load
  namespace: default
spec:
  replicas: 10
  selector:
    matchLabels:
      app: unbalanced-load
  template:
    metadata:
      labels:
        app: unbalanced-load
    spec:
      schedulerName: default-scheduler  # 先用默认调度器造成不均衡
      affinity:
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            preference:
              matchExpressions:
              - key: node-size
                operator: In
                values: ["small"]
      containers:
      - name: nginx
        image: nginx:1.20
        resources:
          requests:
            cpu: "200m"
            memory: "128Mi"
EOF

# 场景2：节点维护模式测试
# 标记节点进入维护模式
kubectl label node <worker-node-name> scheduler.alpha.kubernetes.io/maintenance=true
```

## 🔍 第五步：监控和验证

### 监控调度器日志

```bash
# 查看调度器日志
kubectl logs -n kube-system -l app=rescheduler-scheduler -f

# 过滤重要事件
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep -E "(Filter|Score|PreBind|重调度)"
```

### 监控节点资源使用情况

```bash
# 查看节点资源使用率
kubectl top nodes

# 查看Pod分布
kubectl get pods -o wide --all-namespaces | grep -v kube-system

# 监控Pod调度事件
kubectl get events --sort-by='.lastTimestamp' | grep -E "(Scheduled|FailedScheduling)"
```

### 验证插件功能

```bash
# 1. 验证Filter功能
# 创建大资源需求Pod，应该被过滤到低负载节点
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: test-filter
  namespace: default
spec:
  schedulerName: rescheduler-scheduler
  containers:
  - name: test
    image: nginx:1.20
    resources:
      requests:
        cpu: "1000m"
        memory: "1Gi"
EOF

# 2. 验证Score功能
# 创建多个相同Pod，应该分布到不同节点
for i in {1..5}; do
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: test-score-$i
  namespace: default
spec:
  schedulerName: rescheduler-scheduler
  containers:
  - name: test
    image: nginx:1.20
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
EOF
done

# 3. 验证重调度控制器功能
# 等待30秒后检查是否有Pod被重调度
sleep 30
kubectl get events | grep -i evict
```

## 🧪 第六步：功能测试脚本

创建自动化测试脚本：

```bash
#!/bin/bash
# test-rescheduler.sh

set -e

echo "🚀 开始重调度器功能测试"

# 等待调度器就绪
echo "等待调度器就绪..."
kubectl wait --for=condition=Ready pod -l app=rescheduler-scheduler -n kube-system --timeout=120s

# 测试1：调度优化
echo "📝 测试1: 调度优化功能"
kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-scheduling-optimization
  namespace: default
spec:
  schedulerName: rescheduler-scheduler
  containers:
  - name: nginx
    image: nginx:1.20
    resources:
      requests:
        cpu: "200m"
        memory: "256Mi"
EOF

# 等待Pod调度
kubectl wait --for=condition=PodScheduled pod/test-scheduling-optimization --timeout=30s
echo "✅ 调度优化测试通过"

# 测试2：负载均衡
echo "📝 测试2: 负载均衡功能"
kubectl apply -f manifests/rescheduler/examples/quick-test.yaml

# 等待部署完成
kubectl rollout status deployment/test-deployment --timeout=120s

# 检查Pod分布
echo "Pod分布情况："
kubectl get pods -o wide | grep test-deployment | awk '{print $7}' | sort | uniq -c

# 测试3：重调度控制器
echo "📝 测试3: 重调度控制器功能"
echo "监控重调度事件（60秒）..."
timeout 60 kubectl get events --watch | grep -i evict || true

echo "🎉 所有测试完成！"
```

## 📈 性能测试

### 压力测试

```bash
# 创建大量Pod测试调度性能
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: scheduler-stress-test
  namespace: default
spec:
  replicas: 100
  selector:
    matchLabels:
      app: scheduler-stress-test
  template:
    metadata:
      labels:
        app: scheduler-stress-test
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: pause
        image: k8s.gcr.io/pause:3.9
        resources:
          requests:
            cpu: "10m"
            memory: "16Mi"
EOF

# 监控调度延迟
kubectl get events --sort-by='.lastTimestamp' | grep Scheduled | tail -20
```

## 🧹 清理环境

```bash
# 删除测试资源
kubectl delete deployment --all
kubectl delete pod --all

# 删除调度器
kubectl delete -f manifests/rescheduler/

# 删除Kind集群
kind delete cluster --name rescheduler-test
```

## 📖 配置调优

根据测试结果调整配置参数：

```yaml
# 保守配置（生产环境推荐）
cpuThreshold: 90.0
memoryThreshold: 90.0
enableReschedulingController: false
reschedulingInterval: "60s"

# 激进配置（测试环境）
cpuThreshold: 60.0
memoryThreshold: 60.0
enableReschedulingController: true
reschedulingInterval: "15s"
```

## 🚨 常见问题排查

1. **调度器Pod无法启动**
   - 检查RBAC权限
   - 检查镜像是否正确加载
   - 查看Pod日志

2. **Metrics Server无法获取指标**
   - 确保Metrics Server正常运行
   - 检查网络连接
   - 验证TLS配置

3. **重调度不生效**
   - 确保控制器开关已启用
   - 检查阈值配置是否合理
   - 验证PodDisruptionBudget设置

4. **性能问题**
   - 调整重调度间隔
   - 限制单次重调度Pod数量
   - 优化日志级别
