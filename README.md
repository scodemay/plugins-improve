# HelloScheduler 开发和测试完整指南

## 项目概述

这是一个专为初学者设计的Kubernetes调度插件练手项目。通过这个项目，你将学会：

1. Kubernetes调度框架的基本概念
2. 如何创建自定义调度插件
3. 插件的编译、部署和测试流程
4. 调度插件的调试和日志分析
5. (对重调度的开发在pkg/rescheduler下，可查看详细文档)

## 项目结构

```
scheduler-plugins/
├── pkg/Tinyscheduler/           # 插件源代码
│   ├── hello_scheduler.go        # 主要插件实现
│   └── README.md                 # 插件说明文档
├── manifests/helloscheduler/     # 配置和测试文件
│   ├── scheduler-config.yaml     # 调度器配置
│   └── test-pod.yaml            # 测试Pod定义
├── cmd/scheduler/main.go         # 调度器主程序（已修改）
└── HelloScheduler开发指南.md     # 本文档
```

## 1. 环境准备

### 1.1 基础环境要求

- Go 1.19+ 
- Kubernetes集群（可以是minikube、kind或真实集群）
- kubectl工具
- Docker（用于构建镜像）

### 1.2 检查环境

```bash
# 检查Go版本
go version

# 检查Kubernetes集群
kubectl cluster-info

# 检查节点状态
kubectl get nodes
```

## 2. 代码理解

### 2.1 插件结构说明

HelloScheduler插件实现了`framework.ScorePlugin`接口，主要包含以下方法：

- `Name()`: 返回插件名称
- `Score()`: 为节点计算分数
- `ScoreExtensions()`: 返回分数扩展接口
- `NormalizeScore()`: 标准化分数到框架要求的范围
- `New()`: 插件初始化函数

### 2.2 评分策略

插件使用两个因子计算分数：
1. **节点名称分数**：基于节点名称首字母
2. **资源分数**：基于CPU和内存使用率

### 2.3 关键代码解析

```go
// Score方法是核心评分逻辑
func (hs *TinyScheduler) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeInfo *framework.NodeInfo) (int64, *framework.Status) {
    // 获取节点名称
    nodeName := nodeInfo.Node().Name
    
    // 计算名称分数
    score := int64(150 - nodeName[0])
    
    // 计算资源分数
    cpuUsageRatio := float64(requested.MilliCPU) / float64(allocatable.MilliCPU)
    memUsageRatio := float64(requested.Memory) / float64(allocatable.Memory)
    resourceScore := int64((2.0 - cpuUsageRatio - memUsageRatio) * 50)
    
    return score + resourceScore, nil
}
```

## 3. 编译构建

### 3.1 编译调度器

```bash
# 进入项目根目录
cd /Users/tal/cursor/scheduler-plugins

# 编译调度器二进制文件
make build

# 或者手动编译
go build -o bin/kube-scheduler cmd/scheduler/main.go
```


## 4. 本地测试

### 4.1 准备配置文件

首先修改调度器配置文件中的kubeconfig路径：

```bash
# 获取你的kubeconfig路径
echo $KUBECONFIG
# 或者
ls ~/.kube/config

# 编辑配置文件，替换REPLACE_ME_WITH_KUBE_CONFIG_PATH
vi manifests/helloscheduler/scheduler-config.yaml
```

示例配置：
```yaml
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
leaderElection:
  leaderElect: false
clientConnection:
  kubeconfig: "/Users/tal/.kube/config"  # 替换为你的实际路径
profiles:
  - schedulerName: hello-scheduler
    plugins:
      score:
        enabled:
        - name: TinyScheduler
          weight: 100
```

### 4.2 运行调度器

```bash
# 方式1：直接运行二进制文件
./bin/kube-scheduler --config=manifests/Tinyscheduler/scheduler-config.yaml --v=2

# 方式2：使用go run
go run cmd/scheduler/main.go --config=manifests/Tinyscheduler/scheduler-config.yaml --v=2
```

### 4.3 部署测试Pod

打开新的终端窗口：

```bash
# 部署测试Pod
kubectl apply -f manifests/Tinyscheduler/test-pod.yaml

# 查看Pod状态
kubectl get pods -l app=hello-test

# 查看Pod调度到哪个节点
kubectl get pods -l app=hello-test -o wide

# 查看Pod事件
kubectl describe pod test-pod-1
```

### 4.4 查看调度日志

在运行调度器的终端中，你应该能看到类似这样的日志：

```
I1201 10:30:15.123456 1 hello_scheduler.go:45] HelloScheduler正在计算分数 pod=default/test-pod-1 node=node1
I1201 10:30:15.123456 1 hello_scheduler.go:65] HelloScheduler计算完成 node=node1 nameScore=85 resourceScore=75 finalScore=160 cpuUsage=15.50% memUsage=12.30%
```

## 5. 调试技巧

### 5.1 增加日志级别

```bash
# 使用更详细的日志级别
./bin/kube-scheduler --config=manifests/helloscheduler/scheduler-config.yaml --v=5
```

### 5.2 查看调度结果

```bash
# 查看Pod分布
kubectl get pods -o wide

# 查看节点资源使用情况
kubectl top nodes

# 查看Pod资源请求
kubectl describe nodes
```

### 5.3 常见问题排查

**问题1：Pod一直处于Pending状态**
```bash
# 检查Pod事件
kubectl describe pod <pod-name>

# 检查调度器是否运行
ps aux | grep kube-scheduler

# 检查调度器日志
# 在调度器终端查看错误信息
```

**问题2：找不到hello-scheduler**
```bash
# 确认调度器名称配置正确
grep schedulerName manifests/Tinyscheduler/test-pod.yaml
grep schedulerName manifests/Tinyscheduler/scheduler-config.yaml
```

**问题3：编译错误**
```bash
# 检查Go模块
go mod tidy

# 检查依赖
go mod verify
```

## 6. 进阶测试

### 6.1 性能测试

#创建多个pod
将之前的pod示例随便选择一个镜像，创建十个相同的，然后运行development类型文件


# 观察调度分布
kubectl get pods -o wide | grep test-pod


### 6.2 修改评分策略

你可以修改`pkg/Tinyscheduler/hello_scheduler.go`中的评分逻辑：

```go
// 示例：优先调度到CPU使用率低的节点
func (hs *TinyScheduler) Score(...) (int64, *framework.Status) {
    // 修改这里的逻辑
    cpuScore := int64((1.0 - cpuUsageRatio) * 100)
    return cpuScore, nil
}
```

重新编译和测试：
```bash
# 重新编译
make build

# 重启调度器
./bin/kube-scheduler --config=manifests/helloscheduler/scheduler-config.yaml --v=2
```

## 7. 生产环境部署（高级）

### 7.1 创建Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello-scheduler
  namespace: kube-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hello-scheduler
  template:
    metadata:
      labels:
        app: hello-scheduler
    spec:
      serviceAccountName: system:kube-scheduler
      containers:
      - name: kube-scheduler
        image: hello-scheduler:latest
        command:
        - /usr/local/bin/kube-scheduler
        - --config=/etc/kubernetes/scheduler-config.yaml
        - --v=2
        volumeMounts:
        - name: config
          mountPath: /etc/kubernetes
      volumes:
      - name: config
        configMap:
          name: hello-scheduler-config
```

### 7.2 RBAC配置

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: hello-scheduler
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:kube-scheduler
subjects:
- kind: ServiceAccount
  name: hello-scheduler
  namespace: kube-system
```

## 8. 学习扩展

### 8.1 其他插件接口

尝试实现其他调度接口：
- `FilterPlugin`: 过滤不合适的节点
- `PreFilterPlugin`: 预过滤
- `PostFilterPlugin`: 后过滤
- `PermitPlugin`: 许可控制

### 8.2 参考资料

- [Kubernetes调度框架官方文档](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
- [调度器插件开发指南](https://kubernetes.io/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/)
- [本项目其他插件实现](https://github.com/kubernetes-sigs/scheduler-plugins)

## 9. 清理环境

```bash
# 删除测试Pod
kubectl delete -f manifests/helloscheduler/test-pod.yaml

# 停止调度器（Ctrl+C）

# 删除生成的二进制文件
rm -f bin/kube-scheduler

# 清理Docker镜像（如果构建了）
docker rmi hello-scheduler:latest
```

