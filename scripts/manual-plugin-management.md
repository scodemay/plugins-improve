# Kubernetes调度器插件传统管理方式

## 1. 直接修改配置文件

### 方法一：修改ConfigMap
```bash
# 1. 获取当前配置
kubectl get configmap rescheduler-config -n kube-system -o yaml > current-config.yaml

# 2. 编辑配置文件
vim current-config.yaml

# 3. 应用新配置
kubectl apply -f current-config.yaml

# 4. 重启调度器
kubectl rollout restart deployment/rescheduler-scheduler -n kube-system
```

### 方法二：使用kubectl patch
```bash
# 启用插件
kubectl patch configmap rescheduler-config -n kube-system --type merge -p '{
  "data": {
    "config.yaml": "apiVersion: kubescheduler.config.k8s.io/v1\nkind: KubeSchedulerConfiguration\nprofiles:\n- schedulerName: rescheduler-scheduler\n  plugins:\n    filter:\n      enabled:\n      - name: Rescheduler\n    score:\n      enabled:\n      - name: Rescheduler\n  pluginConfig:\n  - name: Rescheduler\n    args:\n      cpuThreshold: 80.0\n      memoryThreshold: 80.0\n"
  }
}'

# 禁用插件
kubectl patch configmap rescheduler-config -n kube-system --type merge -p '{
  "data": {
    "config.yaml": "apiVersion: kubescheduler.config.k8s.io/v1\nkind: KubeSchedulerConfiguration\nprofiles:\n- schedulerName: rescheduler-scheduler\n  plugins:\n    filter:\n      disabled:\n      - name: Rescheduler\n    score:\n      disabled:\n      - name: Rescheduler\n"
  }
}'
```

## 2. 使用Helm管理

### 创建Helm Chart
```yaml
# values.yaml
scheduler:
  plugins:
    enabled:
      - Rescheduler
      - Coscheduling
    disabled:
      - PrioritySort
  pluginConfig:
    Rescheduler:
      cpuThreshold: 80.0
      memoryThreshold: 80.0
    Coscheduling:
      permitWaitingTimeSeconds: 60
```

### 使用Helm命令
```bash
# 安装/升级
helm install scheduler-plugins ./charts/scheduler-plugins -f values.yaml

# 更新插件配置
helm upgrade scheduler-plugins ./charts/scheduler-plugins -f values.yaml

# 回滚
helm rollback scheduler-plugins 1
```

## 3. 使用Kustomize管理

### 创建Kustomization文件
```yaml
# kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
- base/

patchesStrategicMerge:
- patches/plugins.yaml
- patches/config.yaml

configMapGenerator:
- name: scheduler-config
  literals:
  - config.yaml=|
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
```

### 使用Kustomize
```bash
# 生成配置
kustomize build .

# 应用配置
kubectl apply -k .
```

## 4. 使用GitOps管理

### 创建Git仓库结构
```
scheduler-config/
├── base/
│   ├── kustomization.yaml
│   └── scheduler-config.yaml
├── overlays/
│   ├── dev/
│   │   ├── kustomization.yaml
│   │   └── patches/
│   ├── staging/
│   │   ├── kustomization.yaml
│   │   └── patches/
│   └── prod/
│       ├── kustomization.yaml
│       └── patches/
└── README.md
```

### 使用ArgoCD/Flux
```yaml
# argocd-application.yaml
apiVersion: argocd.io/v1alpha1
kind: Application
metadata:
  name: scheduler-config
spec:
  project: default
  source:
    repoURL: https://github.com/your-org/scheduler-config
    targetRevision: HEAD
    path: overlays/prod
  destination:
    server: https://kubernetes.default.svc
    namespace: kube-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
```

## 5. 使用Operator模式

### 创建自定义资源
```yaml
# scheduler-config-cr.yaml
apiVersion: scheduling.example.com/v1
kind: SchedulerConfig
metadata:
  name: rescheduler-config
spec:
  schedulerName: rescheduler-scheduler
  plugins:
    enabled:
      - name: Rescheduler
        phases: [filter, score]
      - name: Coscheduling
        phases: [permit]
    disabled:
      - name: PrioritySort
        phases: [score]
  pluginConfig:
    Rescheduler:
      cpuThreshold: 80.0
      memoryThreshold: 80.0
    Coscheduling:
      permitWaitingTimeSeconds: 60
```

### 使用kubectl管理
```bash
# 创建配置
kubectl apply -f scheduler-config-cr.yaml

# 更新配置
kubectl patch schedulerconfig rescheduler-config --type merge -p '{
  "spec": {
    "pluginConfig": {
      "Rescheduler": {
        "cpuThreshold": 85.0
      }
    }
  }
}'

# 删除配置
kubectl delete schedulerconfig rescheduler-config
```

## 6. 使用ConfigMap热更新

### 创建热更新脚本
```bash
#!/bin/bash
# hot-update-plugins.sh

NAMESPACE="kube-system"
CONFIGMAP_NAME="rescheduler-config"

# 更新插件配置
update_plugin_config() {
    local plugin_name=$1
    local config_key=$2
    local config_value=$3
    
    # 获取当前配置
    local current_config=$(kubectl get configmap $CONFIGMAP_NAME -n $NAMESPACE -o jsonpath='{.data.config\.yaml}')
    
    # 使用yq更新配置
    local new_config=$(echo "$current_config" | yq eval ".profiles[0].pluginConfig[] | select(.name == \"$plugin_name\") | .args.$config_key = \"$config_value\"" -)
    
    # 应用新配置
    kubectl patch configmap $CONFIGMAP_NAME -n $NAMESPACE --type merge -p "{\"data\":{\"config.yaml\":\"$new_config\"}}"
    
    # 重启调度器
    kubectl rollout restart deployment/rescheduler-scheduler -n $NAMESPACE
}

# 使用示例
update_plugin_config "Rescheduler" "cpuThreshold" "85.0"
```

## 7. 使用Ansible管理

### 创建Ansible Playbook
```yaml
# manage-scheduler-plugins.yml
---
- name: Manage Kubernetes Scheduler Plugins
  hosts: k8s-masters
  tasks:
    - name: Enable Rescheduler plugin
      kubernetes.core.k8s:
        state: present
        definition:
          apiVersion: v1
          kind: ConfigMap
          metadata:
            name: rescheduler-config
            namespace: kube-system
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
                pluginConfig:
                - name: Rescheduler
                  args:
                    cpuThreshold: "{{ cpu_threshold | default('80.0') }}"
                    memoryThreshold: "{{ memory_threshold | default('80.0') }}"
      vars:
        cpu_threshold: "85.0"
        memory_threshold: "90.0"

    - name: Restart scheduler deployment
      kubernetes.core.k8s:
        state: present
        definition:
          apiVersion: apps/v1
          kind: Deployment
          metadata:
            name: rescheduler-scheduler
            namespace: kube-system
          spec:
            replicas: 1
```

### 运行Ansible
```bash
# 运行playbook
ansible-playbook -i inventory manage-scheduler-plugins.yml

# 使用变量
ansible-playbook -i inventory manage-scheduler-plugins.yml -e cpu_threshold=90.0
```

## 8. 使用Terraform管理

### 创建Terraform配置
```hcl
# main.tf
resource "kubernetes_config_map" "scheduler_config" {
  metadata {
    name      = "rescheduler-config"
    namespace = "kube-system"
  }

  data = {
    "config.yaml" = templatefile("${path.module}/scheduler-config.yaml.tpl", {
      cpu_threshold    = var.cpu_threshold
      memory_threshold = var.memory_threshold
      enabled_plugins  = var.enabled_plugins
    })
  }
}

resource "kubernetes_deployment" "scheduler" {
  metadata {
    name      = "rescheduler-scheduler"
    namespace = "kube-system"
  }

  spec {
    replicas = 1
    selector {
      match_labels = {
        app = "rescheduler-scheduler"
      }
    }
    template {
      metadata {
        labels = {
          app = "rescheduler-scheduler"
        }
      }
      spec {
        container {
          name  = "scheduler"
          image = "registry.k8s.io/kube-scheduler:v1.28.0"
          command = ["kube-scheduler"]
          args = [
            "--config=/etc/kubernetes/config.yaml"
          ]
          volume_mount {
            name       = "config"
            mount_path = "/etc/kubernetes"
          }
        }
        volume {
          name = "config"
          config_map {
            name = kubernetes_config_map.scheduler_config.metadata[0].name
          }
        }
      }
    }
  }
}
```

### 使用Terraform
```bash
# 初始化
terraform init

# 规划
terraform plan

# 应用
terraform apply

# 更新配置
terraform apply -var="cpu_threshold=85.0"
```

## 9. 使用Kubernetes原生方式

### 使用Deployment滚动更新
```bash
# 更新ConfigMap
kubectl patch configmap rescheduler-config -n kube-system --type merge -p '{
  "data": {
    "config.yaml": "新的配置内容"
  }
}'

# 触发滚动更新
kubectl patch deployment rescheduler-scheduler -n kube-system -p '{
  "spec": {
    "template": {
      "metadata": {
        "annotations": {
          "kubectl.kubernetes.io/restartedAt": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"
        }
      }
    }
  }
}'
```

### 使用StatefulSet管理
```yaml
# scheduler-statefulset.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: rescheduler-scheduler
  namespace: kube-system
spec:
  serviceName: rescheduler-scheduler
  replicas: 1
  selector:
    matchLabels:
      app: rescheduler-scheduler
  template:
    metadata:
      labels:
        app: rescheduler-scheduler
    spec:
      containers:
      - name: scheduler
        image: registry.k8s.io/kube-scheduler:v1.28.0
        command: ["kube-scheduler"]
        args: ["--config=/etc/kubernetes/config.yaml"]
        volumeMounts:
        - name: config
          mountPath: /etc/kubernetes
  volumeClaimTemplates:
  - metadata:
      name: config
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 1Gi
```

## 10. 使用监控和告警

### 创建监控配置
```yaml
# prometheus-rule.yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: scheduler-plugin-alerts
  namespace: monitoring
spec:
  groups:
  - name: scheduler.plugins
    rules:
    - alert: SchedulerPluginDisabled
      expr: kube_scheduler_plugins_disabled > 0
      for: 5m
      labels:
        severity: warning
      annotations:
        summary: "Scheduler plugin is disabled"
        description: "Plugin {{ $labels.plugin }} is disabled in scheduler {{ $labels.scheduler }}"
    
    - alert: SchedulerConfigChanged
      expr: changes(kube_scheduler_config_hash[5m]) > 0
      for: 1m
      labels:
        severity: info
      annotations:
        summary: "Scheduler configuration changed"
        description: "Scheduler {{ $labels.scheduler }} configuration has changed"
```

## 总结

不同的管理方式各有优缺点：

| 方式 | 优点 | 缺点 | 适用场景 |
|------|------|------|----------|
| 直接修改 | 简单直接 | 容易出错，无版本控制 | 临时修改 |
| Helm | 版本管理，模板化 | 学习成本高 | 生产环境 |
| Kustomize | 配置复用，环境管理 | 功能相对简单 | 多环境部署 |
| GitOps | 版本控制，审计 | 需要额外工具 | 企业级 |
| Operator | 自动化，声明式 | 开发复杂 | 复杂场景 |
| Ansible | 配置管理，幂等性 | 需要Ansible知识 | 混合环境 |
| Terraform | 基础设施即代码 | 状态管理复杂 | 云环境 |
| 原生K8s | 简单，无依赖 | 功能有限 | 简单场景 |

选择合适的管理方式需要考虑团队技能、环境复杂度、维护成本等因素。
