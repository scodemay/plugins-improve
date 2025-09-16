# Kubernetes调度器插件实时管理系统

## 📋 概述

本系统提供了完整的Kubernetes调度器插件实时管理解决方案，支持插件的动态启用、禁用、配置更新，以及可视化的Web管理界面。

## 🚀 功能特性

- ✅ **实时插件管理**: 支持插件的动态启用和禁用，无需重启调度器
- ✅ **配置热更新**: 支持插件配置参数的实时更新
- ✅ **多阶段支持**: 支持在Filter、Score、Reserve、PreBind等不同阶段管理插件
- ✅ **RESTful API**: 提供完整的REST API接口
- ✅ **Web管理界面**: 直观的Web界面，支持可视化操作
- ✅ **配置备份**: 自动备份配置变更，支持回滚
- ✅ **健康检查**: 内置健康检查和监控功能
- ✅ **权限控制**: 完整的RBAC权限管理

## 📁 文件结构

```
scripts/
├── plugin-manager.sh          # 命令行插件管理器
├── plugin-config-api.py       # RESTful API服务
├── plugin-web-ui.html         # Web管理界面
├── deploy-plugin-manager.sh   # 部署脚本
└── README-plugin-manager.md   # 使用说明
```

## 🛠️ 快速开始

### 1. 部署插件管理系统

```bash
# 部署完整的插件管理系统
./scripts/deploy-plugin-manager.sh
```

### 2. 访问Web界面

```bash
# 建立端口转发
kubectl port-forward -n plugin-manager service/plugin-web-ui 3000:3000

# 访问Web界面
open http://localhost:3000
```

### 3. 使用命令行工具

```bash
# 交互式模式
./scripts/plugin-manager.sh

# 命令行模式
./scripts/plugin-manager.sh enable Rescheduler filter,score
./scripts/plugin-manager.sh disable Coscheduling filter
./scripts/plugin-manager.sh update Rescheduler cpuThreshold 80.0
```

## 📖 详细使用说明

### 命令行工具使用

#### 交互式模式

```bash
./scripts/plugin-manager.sh
```

交互式菜单选项：
- `1` - 启用插件
- `2` - 禁用插件  
- `3` - 更新插件配置
- `4` - 列出插件状态
- `5` - 重启调度器
- `6` - 显示帮助
- `7` - 退出

#### 命令行模式

```bash
# 启用插件
./scripts/plugin-manager.sh enable <plugin_name> [phases]

# 禁用插件
./scripts/plugin-manager.sh disable <plugin_name> [phases]

# 更新插件配置
./scripts/plugin-manager.sh update <plugin_name> <config_key> <config_value>

# 列出插件状态
./scripts/plugin-manager.sh list

# 重启调度器
./scripts/plugin-manager.sh restart
```

### RESTful API使用

#### 基础URL
```
http://localhost:8080/api/v1
```

#### API接口

##### 1. 获取插件状态
```bash
GET /api/v1/plugins
```

响应示例：
```json
{
  "status": "success",
  "data": {
    "enabled_plugins": {
      "filter": ["Rescheduler"],
      "score": ["Rescheduler"]
    },
    "disabled_plugins": {
      "filter": ["Coscheduling"]
    },
    "plugin_configs": {
      "Rescheduler": {
        "cpuThreshold": 80.0,
        "memoryThreshold": 80.0
      }
    }
  },
  "supported_plugins": ["Rescheduler", "Coscheduling", ...],
  "supported_phases": ["filter", "score", "reserve", "preBind"]
}
```

##### 2. 启用插件
```bash
POST /api/v1/plugins/{plugin_name}/enable
Content-Type: application/json

{
  "phases": ["filter", "score"]
}
```

##### 3. 禁用插件
```bash
POST /api/v1/plugins/{plugin_name}/disable
Content-Type: application/json

{
  "phases": ["filter", "score"]
}
```

##### 4. 更新插件配置
```bash
PUT /api/v1/plugins/{plugin_name}/config
Content-Type: application/json

{
  "cpuThreshold": 85.0,
  "memoryThreshold": 90.0
}
```

##### 5. 重启调度器
```bash
POST /api/v1/scheduler/restart
```

##### 6. 健康检查
```bash
GET /api/v1/health
```

##### 7. 备份配置
```bash
POST /api/v1/config/backup
```

### Web界面使用

#### 访问Web界面
1. 打开浏览器访问 `http://localhost:3000`
2. 界面包含以下功能：
   - **插件操作**: 启用/禁用插件，选择插件阶段
   - **插件配置**: 修改插件配置参数
   - **插件状态**: 查看所有插件的当前状态
   - **实时更新**: 自动刷新插件状态

#### 操作步骤
1. **启用插件**:
   - 选择要启用的插件
   - 选择插件阶段（Filter、Score等）
   - 点击"启用插件"按钮

2. **禁用插件**:
   - 选择要禁用的插件
   - 选择插件阶段
   - 点击"禁用插件"按钮

3. **配置插件**:
   - 选择要配置的插件
   - 修改配置参数
   - 点击"保存配置"按钮

4. **查看状态**:
   - 在插件状态面板查看所有插件的当前状态
   - 支持实时刷新

## 🔧 支持的插件和阶段

### 支持的插件
- Rescheduler - 重调度器
- Coscheduling - 协同调度
- CapacityScheduling - 容量调度
- NodeResourceTopologyMatch - 节点资源拓扑匹配
- NodeResourcesAllocatable - 节点资源可分配
- TargetLoadPacking - 目标负载打包
- LoadVariationRiskBalancing - 负载变化风险平衡
- PreemptionToleration - 抢占容忍
- PodState - Pod状态
- QoS - 服务质量
- SySched - 系统调度
- Trimaran - 三色调度

### 支持的阶段
- **filter** - 过滤阶段
- **score** - 评分阶段
- **reserve** - 预留阶段
- **preBind** - 预绑定阶段
- **preFilter** - 预过滤阶段
- **postFilter** - 后过滤阶段
- **permit** - 许可阶段
- **bind** - 绑定阶段
- **postBind** - 后绑定阶段

## ⚙️ 配置说明

### 环境变量

#### API服务配置
```bash
KUBERNETES_NAMESPACE=kube-system      # Kubernetes命名空间
CONFIGMAP_NAME=rescheduler-config     # ConfigMap名称
SCHEDULER_DEPLOYMENT=rescheduler-scheduler  # 调度器部署名称
PORT=8080                             # API服务端口
HOST=0.0.0.0                         # API服务主机
```

#### 命令行工具配置
```bash
NAMESPACE="kube-system"               # 目标命名空间
CONFIGMAP_NAME="rescheduler-config"   # ConfigMap名称
SCHEDULER_DEPLOYMENT="rescheduler-scheduler"  # 调度器部署名称
```

### 权限要求

系统需要以下Kubernetes权限：
- `configmaps`: get, list, watch, update, patch
- `pods`: get, list, watch
- `nodes`: get, list, watch
- `deployments`: get, list, watch, update, patch
- `replicasets`: get, list, watch
- `events`: get, list, watch

## 🔍 故障排除

### 常见问题

#### 1. 插件启用失败
```bash
# 检查调度器状态
kubectl get pods -n kube-system -l app=rescheduler-scheduler

# 检查ConfigMap
kubectl get configmap rescheduler-config -n kube-system -o yaml

# 查看调度器日志
kubectl logs -n kube-system -l app=rescheduler-scheduler
```

#### 2. API服务无法访问
```bash
# 检查API服务状态
kubectl get pods -n plugin-manager -l app=plugin-config-api

# 检查服务配置
kubectl get service plugin-config-api -n plugin-manager

# 查看API服务日志
kubectl logs -n plugin-manager -l app=plugin-config-api
```

#### 3. Web界面无法访问
```bash
# 检查Web服务状态
kubectl get pods -n plugin-manager -l app=plugin-web-ui

# 检查端口转发
kubectl port-forward -n plugin-manager service/plugin-web-ui 3000:3000
```

#### 4. 权限问题
```bash
# 检查RBAC配置
kubectl get clusterrole plugin-manager-role
kubectl get clusterrolebinding plugin-manager-binding

# 检查ServiceAccount
kubectl get serviceaccount plugin-manager-sa -n plugin-manager
```

### 调试模式

#### 启用详细日志
```bash
# API服务调试
kubectl logs -n plugin-manager -l app=plugin-config-api -f

# 调度器调试
kubectl logs -n kube-system -l app=rescheduler-scheduler -f
```

#### 手动测试API
```bash
# 测试健康检查
curl http://localhost:8080/api/v1/health

# 测试获取插件状态
curl http://localhost:8080/api/v1/plugins

# 测试启用插件
curl -X POST http://localhost:8080/api/v1/plugins/Rescheduler/enable \
  -H "Content-Type: application/json" \
  -d '{"phases": ["filter", "score"]}'
```

## 📈 性能优化

### 建议配置

#### API服务资源限制
```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 200m
    memory: 256Mi
```

#### 调度器资源限制
```yaml
resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

### 监控指标

系统提供以下监控指标：
- 插件启用/禁用次数
- 配置更新频率
- API响应时间
- 调度器重启次数
- 错误率统计

## 🔒 安全考虑

### 网络安全
- API服务使用ClusterIP，仅集群内访问
- Web界面使用NodePort，可配置防火墙规则
- 支持HTTPS配置（需要证书）

### 权限控制
- 最小权限原则
- 定期权限审计
- 敏感操作需要确认

### 数据安全
- 配置自动备份
- 支持配置加密
- 审计日志记录

## 🚀 扩展功能

### 计划中的功能
1. **多集群支持**: 支持管理多个Kubernetes集群
2. **插件市场**: 支持第三方插件安装和管理
3. **配置模板**: 预定义的配置模板
4. **批量操作**: 支持批量启用/禁用插件
5. **配置版本管理**: 配置变更历史记录
6. **告警系统**: 插件异常告警
7. **性能分析**: 插件性能分析报告

### 自定义开发
系统采用模块化设计，支持自定义开发：
- 自定义插件类型
- 自定义配置参数
- 自定义API接口
- 自定义Web界面

## 📞 支持与反馈

如有问题或建议，请通过以下方式联系：
- 提交Issue到项目仓库
- 发送邮件到项目维护者
- 参与项目讨论

## 📄 许可证

本项目遵循与主项目相同的许可证。
