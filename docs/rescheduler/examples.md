# 📚 重调度器使用示例

## 📋 概述

本文档提供重调度器插件的实际使用示例，涵盖各种常见场景和最佳实践。

## 🚀 基础使用示例

### 1. 简单Pod调度
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: example-pod
  labels:
    app: example
spec:
  schedulerName: rescheduler-scheduler  # 指定使用重调度器
  containers:
  - name: nginx
    image: nginx:latest
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 200m
        memory: 256Mi
```

### 2. Deployment使用重调度器
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-app
  labels:
    app: web-app
spec:
  replicas: 6
  selector:
    matchLabels:
      app: web-app
  template:
    metadata:
      labels:
        app: web-app
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: web
        image: nginx:alpine
        ports:
        - containerPort: 80
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 100m
            memory: 128Mi
```

## 🎯 高级使用场景

### 1. 排除重调度的关键应用
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: critical-database
  labels:
    app: database
    tier: critical
    # 关键标签：排除重调度
    scheduler.alpha.kubernetes.io/rescheduling: "disabled"
spec:
  schedulerName: rescheduler-scheduler
  containers:
  - name: postgres
    image: postgres:13
    env:
    - name: POSTGRES_DB
      value: myapp
    - name: POSTGRES_USER
      value: user
    - name: POSTGRES_PASSWORD
      valueFrom:
        secretKeyRef:
          name: db-secret
          key: password
    resources:
      requests:
        cpu: 500m
        memory: 1Gi
      limits:
        cpu: 1000m
        memory: 2Gi
    volumeMounts:
    - name: data
      mountPath: /var/lib/postgresql/data
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: database-pvc
```

### 2. 高优先级应用（不易被重调度）
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: priority-service
  labels:
    app: priority-service
    priority: high
spec:
  schedulerName: rescheduler-scheduler
  priorityClassName: high-priority
  containers:
  - name: service
    image: myapp:latest
    resources:
      requests:
        cpu: 200m
        memory: 256Mi
      limits:
        cpu: 500m
        memory: 512Mi
```

### 3. 测试和开发工作负载（易被重调度）
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-workload
  namespace: development
  labels:
    app: test-workload
    env: development
spec:
  replicas: 10
  selector:
    matchLabels:
      app: test-workload
  template:
    metadata:
      labels:
        app: test-workload
        env: development
        # 允许积极重调度的标签
        scheduler.alpha.kubernetes.io/rescheduling: "enabled"
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: test-app
        image: busybox:latest
        command: ["sleep", "3600"]
        resources:
          requests:
            cpu: 10m
            memory: 32Mi
          limits:
            cpu: 50m
            memory: 64Mi
```

## 🔧 节点维护场景

### 1. 标记节点进入维护模式
```bash
# 标记节点为维护模式
kubectl label node worker-1 scheduler.alpha.kubernetes.io/maintenance=true

# 验证标签
kubectl get nodes --show-labels | grep maintenance

# 观察Pod迁移过程
kubectl get pods -o wide --watch
```

### 2. 创建维护窗口部署
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: node-maintenance
spec:
  template:
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: maintenance
        image: alpine:latest
        command:
        - /bin/sh
        - -c
        - |
          echo "开始节点维护操作..."
          # 标记节点维护
          kubectl label node ${TARGET_NODE} scheduler.alpha.kubernetes.io/maintenance=true
          
          # 等待Pod迁移完成
          echo "等待Pod迁移..."
          sleep 300
          
          # 执行维护操作
          echo "执行节点维护..."
          # 这里添加实际的维护脚本
          
          # 完成后取消维护标记
          kubectl label node ${TARGET_NODE} scheduler.alpha.kubernetes.io/maintenance-
          echo "节点维护完成"
        env:
        - name: TARGET_NODE
          value: "worker-1"
      restartPolicy: Never
      serviceAccountName: node-maintenance-sa
```

## 📊 性能测试场景

### 1. 负载均衡测试
```yaml
# 创建大量Pod测试负载均衡
apiVersion: apps/v1
kind: Deployment
metadata:
  name: load-balance-test
  namespace: test
spec:
  replicas: 30  # 足够的副本数触发重调度
  selector:
    matchLabels:
      app: load-test
  template:
    metadata:
      labels:
        app: load-test
        test-type: load-balancing
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: load-generator
        image: nginx:alpine
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 100m
            memory: 128Mi
        ports:
        - containerPort: 80
```

### 2. 资源压力测试
```yaml
apiVersion: v1
kind: Pod
metadata:
  name: stress-test
  namespace: test
  labels:
    test-type: resource-stress
spec:
  schedulerName: rescheduler-scheduler
  containers:
  - name: cpu-stress
    image: progrium/stress
    args: ["--cpu", "2", "--timeout", "600s"]  # 2核CPU压力10分钟
    resources:
      requests:
        cpu: 1500m
        memory: 512Mi
      limits:
        cpu: 2000m
        memory: 1Gi
  - name: memory-stress
    image: progrium/stress  
    args: ["--vm", "1", "--vm-bytes", "512M", "--timeout", "600s"]
    resources:
      requests:
        cpu: 100m
        memory: 600Mi
      limits:
        cpu: 200m
        memory: 800Mi
```

## 🎮 完整应用示例

### 1. 微服务应用栈
```yaml
---
# Frontend 服务
apiVersion: apps/v1
kind: Deployment
metadata:
  name: frontend
  labels:
    app: myapp
    tier: frontend
spec:
  replicas: 3
  selector:
    matchLabels:
      app: myapp
      tier: frontend
  template:
    metadata:
      labels:
        app: myapp
        tier: frontend
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: frontend
        image: nginx:alpine
        ports:
        - containerPort: 80
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 100m
            memory: 128Mi

---
# Backend API 服务
apiVersion: apps/v1
kind: Deployment
metadata:
  name: backend
  labels:
    app: myapp
    tier: backend
spec:
  replicas: 4
  selector:
    matchLabels:
      app: myapp
      tier: backend
  template:
    metadata:
      labels:
        app: myapp
        tier: backend
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: api
        image: node:16-alpine
        ports:
        - containerPort: 3000
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 200m
            memory: 256Mi

---
# 数据库（排除重调度）
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: database
  labels:
    app: myapp
    tier: database
spec:
  serviceName: database
  replicas: 1
  selector:
    matchLabels:
      app: myapp
      tier: database
  template:
    metadata:
      labels:
        app: myapp
        tier: database
        # 排除重调度
        scheduler.alpha.kubernetes.io/rescheduling: "disabled"
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: postgres
        image: postgres:13
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_DB
          value: myapp
        - name: POSTGRES_USER
          value: postgres
        - name: POSTGRES_PASSWORD
          value: secretpassword
        resources:
          requests:
            cpu: 500m
            memory: 1Gi
          limits:
            cpu: 1000m
            memory: 2Gi
        volumeMounts:
        - name: data
          mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 10Gi
```

### 2. 批处理作业
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: data-processing
  labels:
    app: batch-processing
spec:
  parallelism: 5  # 并行5个Pod
  completions: 20 # 总共20个任务
  template:
    metadata:
      labels:
        app: batch-processing
        job-type: data-processing
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: processor
        image: python:3.9-slim
        command:
        - python
        - -c
        - |
          import time
          import random
          
          # 模拟数据处理
          processing_time = random.randint(60, 300)  # 1-5分钟
          print(f"开始处理数据，预计耗时 {processing_time} 秒")
          
          for i in range(processing_time):
            if i % 30 == 0:
              print(f"处理进度: {i}/{processing_time}")
            time.sleep(1)
          
          print("数据处理完成")
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: 500m
            memory: 512Mi
      restartPolicy: Never
  backoffLimit: 3
```

## 🔍 监控和观察示例

### 1. 监控重调度行为
```bash
#!/bin/bash
# 重调度监控脚本

echo "开始监控重调度器行为..."

while true; do
  echo "=== $(date) ==="
  
  # Pod分布统计
  echo "📊 当前Pod分布:"
  kubectl get pods --all-namespaces -o wide | \
    awk 'NR>1 {print $8}' | sort | uniq -c | \
    while read count node; do
      echo "  $node: $count pods"
    done
  
  # 节点资源使用
  echo "💾 节点资源使用:"
  kubectl top nodes 2>/dev/null || echo "  metrics-server未安装"
  
  # 最近重调度事件
  echo "🔄 最近重调度 (30秒内):"
  kubectl logs -n kube-system -l app=rescheduler-scheduler --since=30s | \
    grep "重调度\|migration" | tail -5
  
  echo "---"
  sleep 30
done
```

### 2. 性能压测脚本
```bash
#!/bin/bash
# 重调度器性能测试

echo "开始性能测试..."

# 部署测试工作负载
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: perf-test
spec:
  replicas: 50
  selector:
    matchLabels:
      app: perf-test
  template:
    metadata:
      labels:
        app: perf-test
    spec:
      schedulerName: rescheduler-scheduler
      containers:
      - name: app
        image: busybox:latest
        command: ["sleep", "3600"]
        resources:
          requests:
            cpu: 10m
            memory: 32Mi
EOF

echo "等待Pod调度完成..."
sleep 60

# 统计调度时间
echo "📈 调度性能统计:"
kubectl get events --sort-by='.lastTimestamp' | \
  grep "Scheduled.*perf-test" | \
  tail -10 | \
  awk '{print $1, $2, $6, $7, $8, $9, $10}'

# 检查Pod分布
echo "📊 Pod分布情况:"
kubectl get pods -l app=perf-test -o wide | \
  awk 'NR>1 {print $7}' | sort | uniq -c

# 清理
echo "🧹 清理测试资源..."
kubectl delete deployment perf-test
```

## 📋 最佳实践示例

### 1. 生产环境部署模板
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: production-app
  labels:
    app: production-app
    env: production
spec:
  replicas: 6
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
      maxSurge: 1
  selector:
    matchLabels:
      app: production-app
  template:
    metadata:
      labels:
        app: production-app
        env: production
      annotations:
        # 部署信息
        deployment.kubernetes.io/revision: "1"
        app.kubernetes.io/version: "v1.0.0"
    spec:
      schedulerName: rescheduler-scheduler
      
      # 安全上下文
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
      
      containers:
      - name: app
        image: myapp:v1.0.0
        ports:
        - containerPort: 8080
          name: http
        
        # 资源配置
        resources:
          requests:
            cpu: 200m
            memory: 256Mi
          limits:
            cpu: 500m
            memory: 512Mi
        
        # 健康检查
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
        
        # 环境变量
        env:
        - name: ENV
          value: "production"
        - name: LOG_LEVEL
          value: "info"
      
      # 拓扑分布约束
      topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: kubernetes.io/hostname
        whenUnsatisfiable: DoNotSchedule
        labelSelector:
          matchLabels:
            app: production-app
      
      # 亲和性配置
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchLabels:
                  app: production-app
              topologyKey: kubernetes.io/hostname
```

### 2. 开发环境快速迭代
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dev-app
  namespace: development
  labels:
    app: dev-app
    env: development
spec:
  replicas: 2
  selector:
    matchLabels:
      app: dev-app
  template:
    metadata:
      labels:
        app: dev-app
        env: development
        # 允许积极重调度
        scheduler.alpha.kubernetes.io/rescheduling: "enabled"
    spec:
      schedulerName: rescheduler-scheduler
      
      containers:
      - name: app
        image: dev/myapp:latest
        imagePullPolicy: Always  # 开发环境总是拉取最新镜像
        ports:
        - containerPort: 8080
        
        # 开发环境较低的资源配置
        resources:
          requests:
            cpu: 50m
            memory: 128Mi
          limits:
            cpu: 200m
            memory: 256Mi
        
        # 快速启动配置
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        
        env:
        - name: ENV
          value: "development"
        - name: LOG_LEVEL
          value: "debug"
        - name: HOT_RELOAD
          value: "true"
```

---

**相关文档**: [README](./README.md) | [部署指南](./deployment-guide.md) | [配置参考](./configuration.md) | [故障排除](./troubleshooting.md)
