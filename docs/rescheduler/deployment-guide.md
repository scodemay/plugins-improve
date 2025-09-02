# 🚀 重调度器部署指南

## 📋 环境准备

### 前置条件
- Kubernetes 集群 (v1.20+)
- kubectl 命令行工具
- Docker 环境
- Go 1.21+ (用于构建)

### 集群要求
- 至少 2 个 worker 节点（用于重调度测试）
- 节点需要安装 metrics-server（用于资源监控）

## 🛠️ 第一步：环境搭建

### Option 1: Kind 集群（推荐开发测试）
```bash
# 创建 Kind 集群配置
cat > kind-config.yaml << EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: rescheduler-demo
nodes:
- role: control-plane
- role: worker
- role: worker  
- role: worker
EOF

# 创建集群
kind create cluster --config kind-config.yaml

# 验证集群
kubectl get nodes
```

### Option 2: 现有 Kubernetes 集群
```bash
# 验证集群访问
kubectl cluster-info

# 检查节点状态
kubectl get nodes -o wide

# 安装 metrics-server（如果未安装）
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
```

## 📦 第二步：构建和准备镜像

### 构建调度器
```bash
# 进入项目目录
cd scheduler-plugins

# 构建二进制文件
make build-scheduler

# 构建 Docker 镜像
docker build -f Dockerfile.local -t scheduler-plugins:latest .

# 如果使用 Kind，加载镜像到集群
kind load docker-image scheduler-plugins:latest --name rescheduler-demo
```

### 验证构建
```bash
# 检查二进制文件
ls -la bin/

# 检查镜像
docker images | grep scheduler-plugins
```

## 🚀 第三步：部署重调度器

### 快速部署（推荐）
```bash
# 部署所有组件
kubectl apply -f manifests/rescheduler/

# 验证部署
kubectl get pods -n kube-system -l app=rescheduler-scheduler

# 检查调度器状态
kubectl logs -n kube-system -l app=rescheduler-scheduler
```

### 分步部署（自定义配置）
```bash
# 1. 创建 RBAC
kubectl apply -f manifests/rescheduler/rbac.yaml

# 2. 创建配置
kubectl apply -f manifests/rescheduler/config.yaml

# 3. 部署调度器
kubectl apply -f manifests/rescheduler/scheduler.yaml

# 4. 创建优先级类（可选）
kubectl apply -f manifests/rescheduler/priority-classes.yaml
```

### 验证部署
```bash
# 检查 Pod 状态
kubectl get pods -n kube-system -l app=rescheduler-scheduler

# 查看详细状态
kubectl describe deployment -n kube-system rescheduler-scheduler

# 检查日志
kubectl logs -n kube-system -l app=rescheduler-scheduler -f
```

## 🔧 第四步：配置调优

### 基础配置调整
```bash
# 编辑配置
kubectl edit configmap -n kube-system rescheduler-config

# 重启调度器应用配置
kubectl rollout restart deployment -n kube-system rescheduler-scheduler
```

### 常用配置模板

#### 保守模式配置（生产环境推荐）
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: rescheduler-config
  namespace: kube-system
data:
  config.yaml: |
    pluginConfig:
    - name: Rescheduler
      args:
        reschedulingInterval: "60s"              # 降低检查频率
        enabledStrategies: ["LoadBalancing"]     # 仅启用负载均衡
        cpuThreshold: 90.0                       # 提高阈值
        memoryThreshold: 90.0
        maxReschedulePods: 5                     # 限制重调度数量
        enableSchedulingOptimization: true       # 启用调度优化
        enablePreventiveRescheduling: false      # 关闭预防性重调度
```

#### 积极模式配置（测试环境）
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: rescheduler-config
  namespace: kube-system
data:
  config.yaml: |
    pluginConfig:
    - name: Rescheduler
      args:
        reschedulingInterval: "30s"
        enabledStrategies: 
          - "LoadBalancing"
          - "ResourceOptimization"
          - "NodeMaintenance"
        cpuThreshold: 70.0
        memoryThreshold: 70.0
        maxReschedulePods: 10
        enableSchedulingOptimization: true
        enablePreventiveRescheduling: true       # 启用所有功能
```

## 🧪 第五步：功能测试

### 部署测试工作负载
```bash
# 部署测试应用
kubectl apply -f manifests/rescheduler/examples/quick-test.yaml

# 观察 Pod 分布
kubectl get pods -o wide
```

### 测试场景

#### 1. 负载均衡测试
```bash
# 部署不均衡的 Pod
kubectl apply -f - << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: load-test
spec:
  replicas: 10
  selector:
    matchLabels:
      app: load-test
  template:
    metadata:
      labels:
        app: load-test
    spec:
      schedulerName: rescheduler-scheduler
      nodeSelector:
        node-role.kubernetes.io/worker: ""
      containers:
      - name: nginx
        image: nginx:latest
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
EOF

# 观察重调度行为
kubectl logs -n kube-system -l app=rescheduler-scheduler -f
```

#### 2. 节点维护测试
```bash
# 标记节点为维护模式
kubectl label node <worker-node> scheduler.alpha.kubernetes.io/maintenance=true

# 观察 Pod 迁移
kubectl get pods -o wide --watch

# 取消维护模式
kubectl label node <worker-node> scheduler.alpha.kubernetes.io/maintenance-
```

#### 3. 调度优化测试
```bash
# 部署使用重调度器的应用
kubectl apply -f - << EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-scheduling
spec:
  schedulerName: rescheduler-scheduler
  containers:
  - name: app
    image: nginx:latest
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
EOF

# 查看调度决策日志
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "test-scheduling"
```

## 📊 第六步：监控和观察

### 关键监控指标
```bash
# 查看节点资源使用
kubectl top nodes

# 查看 Pod 资源使用
kubectl top pods --all-namespaces

# 观察 Pod 分布
kubectl get pods --all-namespaces -o wide | \
  awk 'NR>1 {print $8}' | sort | uniq -c

# 查看重调度器日志
kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=100
```

### 设置日志级别
```bash
# 增加日志详细程度
kubectl patch deployment -n kube-system rescheduler-scheduler \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-scheduler","args":["--config=/etc/kubernetes/config.yaml","--v=4"]}]}}}}'
```

## 🔧 故障排除

### 常见问题

#### 1. 调度器启动失败
```bash
# 检查 Pod 状态
kubectl describe pod -n kube-system -l app=rescheduler-scheduler

# 检查配置文件
kubectl get configmap -n kube-system rescheduler-config -o yaml

# 检查 RBAC 权限
kubectl auth can-i create pods --as=system:serviceaccount:kube-system:rescheduler-scheduler
```

#### 2. Pod 无法调度
```bash
# 检查调度器事件
kubectl get events --field-selector involvedObject.kind=Pod

# 查看调度器日志
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep ERROR

# 验证调度器注册
kubectl get pods -A -o wide | grep rescheduler-scheduler
```

#### 3. 重调度不工作
```bash
# 检查重调度器是否运行
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep "重调度器开始运行"

# 验证节点资源使用
kubectl top nodes

# 检查 Pod 标签（排除标签会阻止重调度）
kubectl get pods --show-labels | grep rescheduling
```

## 🧹 清理和卸载

### 清理测试资源
```bash
# 删除测试应用
kubectl delete deployment load-test
kubectl delete -f manifests/rescheduler/examples/quick-test.yaml

# 移除节点标签
kubectl label node --all scheduler.alpha.kubernetes.io/maintenance-
```

### 完全卸载
```bash
# 删除重调度器
kubectl delete -f manifests/rescheduler/

# 验证清理
kubectl get pods -n kube-system | grep rescheduler
```

### Kind 集群清理
```bash
# 删除 Kind 集群
kind delete cluster --name rescheduler-demo
```

## 📈 生产环境部署建议

### 1. 高可用配置
```yaml
# 高可用部署示例
apiVersion: apps/v1
kind: Deployment
spec:
  replicas: 2  # 多副本
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
```

### 2. 资源限制
```yaml
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

### 3. 监控集成
```yaml
# 添加 Prometheus 注解
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "10259"
    prometheus.io/path: "/metrics"
```

### 4. 安全配置
```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65534
  readOnlyRootFilesystem: true
```

---

**下一步**：查看 [配置参考](./configuration.md) 了解详细的配置选项
