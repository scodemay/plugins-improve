# 🛠️ 重调度器开发指南

## 📋 开发环境搭建

### 前置条件
- Go 1.21+
- Docker
- kubectl
- Kind (推荐) 或其他Kubernetes集群

### 开发环境准备
```bash
# 1. 克隆项目
git clone <scheduler-plugins-repo>
cd scheduler-plugins

# 2. 安装依赖
go mod download

# 3. 验证构建
make build-scheduler

# 4. 创建开发集群
kind create cluster --config manifests/rescheduler/examples/kind-config.yaml
```

## 🔧 代码结构

### 核心文件组织
```
pkg/rescheduler/
├── rescheduler.go              # 主要插件实现
├── deployment_coordinator.go   # Deployment协调器
├── controller.go              # 控制器逻辑
└── types.go                   # 类型定义

docs/rescheduler/
├── README.md                  # 项目概述
├── deployment-guide.md        # 部署指南
├── configuration.md           # 配置参考
├── examples.md               # 使用示例
├── troubleshooting.md        # 故障排除
└── development.md            # 开发指南

manifests/rescheduler/
├── rbac.yaml                 # RBAC配置
├── config.yaml               # 调度器配置
├── scheduler.yaml            # 调度器部署
├── kustomization.yaml        # Kustomize配置
└── examples/                 # 示例配置
    ├── quick-test.yaml       # 快速测试
    └── configuration-examples.yaml  # 配置示例
```

### 关键接口实现

#### Filter接口
```go
func (r *Rescheduler) Filter(
    ctx context.Context, 
    state *framework.CycleState, 
    pod *v1.Pod, 
    nodeInfo *framework.NodeInfo,
) *framework.Status {
    // 实现节点过滤逻辑
    // 1. 检查节点资源使用率
    // 2. 检查维护模式
    // 3. 返回过滤结果
}
```

#### Score接口
```go
func (r *Rescheduler) Score(
    ctx context.Context, 
    state *framework.CycleState, 
    pod *v1.Pod, 
    nodeName string,
) (int64, *framework.Status) {
    // 实现节点打分逻辑
    // 1. 计算CPU/内存使用率分数
    // 2. 应用权重配置
    // 3. 添加负载均衡奖励
}
```

#### PreBind接口
```go
func (r *Rescheduler) PreBind(
    ctx context.Context, 
    state *framework.CycleState, 
    pod *v1.Pod, 
    nodeName string,
) *framework.Status {
    // 实现预防性重调度逻辑
    // 1. 预测调度后负载
    // 2. 判断是否需要预防性重调度
    // 3. 异步触发重调度操作
}
```

## 🧪 开发调试

### 本地调试设置
```bash
# 1. 构建调试版本
go build -tags debug -o bin/kube-scheduler-debug cmd/scheduler/main.go

# 2. 创建调试配置
cat > debug-config.yaml << EOF
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: debug-scheduler
  plugins:
    filter:
      enabled: [name: Rescheduler]
    score:
      enabled: [name: Rescheduler]
    preBind:
      enabled: [name: Rescheduler]
  pluginConfig:
  - name: Rescheduler
    args:
      reschedulingInterval: "10s"  # 短间隔便于调试
      enabledStrategies: ["LoadBalancing"]
      cpuThreshold: 50.0
      memoryThreshold: 50.0
EOF

# 3. 本地运行调度器
./bin/kube-scheduler-debug --config=debug-config.yaml --v=4
```

### 单元测试
```bash
# 运行特定包的测试
go test ./pkg/rescheduler -v

# 运行带覆盖率的测试
go test ./pkg/rescheduler -coverprofile=coverage.out
go tool cover -html=coverage.out

# 运行基准测试
go test ./pkg/rescheduler -bench=. -benchmem
```

### 集成测试
```bash
# 部署测试环境
kubectl apply -f manifests/rescheduler/

# 运行集成测试
go test ./test/integration/rescheduler -v

# 清理测试环境
kubectl delete -f manifests/rescheduler/
```

## 🔍 调试技巧

### 日志调试
```go
// 在代码中添加调试日志
klog.V(4).InfoS("调试信息", "pod", pod.Name, "node", nodeName)
klog.V(2).InfoS("重要事件", "action", "rescheduling", "reason", reason)

// 运行时启用详细日志
--v=4  # 详细调试信息
--v=2  # 关键事件信息
```

### 性能分析
```go
// 添加性能分析点
import _ "net/http/pprof"

// 在main函数中启用
go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

### 内存分析
```bash
# 获取内存配置文件
go tool pprof http://localhost:6060/debug/pprof/heap

# 分析CPU使用
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30
```

## 🧪 测试策略

### 测试分层
```
Unit Tests (单元测试)
├── Filter逻辑测试
├── Score计算测试
├── 重调度决策测试
└── 配置解析测试

Integration Tests (集成测试)  
├── 调度器端到端测试
├── 重调度流程测试
├── 配置变更测试
└── 性能基准测试

E2E Tests (端到端测试)
├── 真实集群部署测试
├── 多场景功能测试
├── 故障恢复测试
└── 升级兼容性测试
```

### 单元测试示例
```go
func TestFilterOverloadedNode(t *testing.T) {
    // 创建测试调度器
    r := &Rescheduler{
        config: &ReschedulerConfig{
            CPUThreshold:    80.0,
            MemoryThreshold: 80.0,
        },
    }
    
    // 创建过载节点
    nodeInfo := &framework.NodeInfo{}
    nodeInfo.SetNode(&v1.Node{
        ObjectMeta: metav1.ObjectMeta{Name: "test-node"},
        Status: v1.NodeStatus{
            Capacity: v1.ResourceList{
                v1.ResourceCPU:    resource.MustParse("2"),
                v1.ResourceMemory: resource.MustParse("4Gi"),
            },
        },
    })
    
    // 添加高资源使用的Pod
    for i := 0; i < 5; i++ {
        pod := &v1.Pod{
            Spec: v1.PodSpec{
                Containers: []v1.Container{{
                    Resources: v1.ResourceRequirements{
                        Requests: v1.ResourceList{
                            v1.ResourceCPU:    resource.MustParse("400m"),
                            v1.ResourceMemory: resource.MustParse("800Mi"),
                        },
                    },
                }},
            },
        }
        nodeInfo.AddPod(pod)
    }
    
    // 测试新Pod是否被过滤
    testPod := &v1.Pod{
        Spec: v1.PodSpec{
            Containers: []v1.Container{{
                Resources: v1.ResourceRequirements{
                    Requests: v1.ResourceList{
                        v1.ResourceCPU:    resource.MustParse("200m"),
                        v1.ResourceMemory: resource.MustParse("400Mi"),
                    },
                },
            }},
        },
    }
    
    status := r.Filter(context.Background(), nil, testPod, nodeInfo)
    assert.False(t, status.IsSuccess(), "过载节点应该被过滤")
}
```

### 集成测试示例
```go
func TestReschedulingWorkflow(t *testing.T) {
    // 创建测试集群客户端
    clientset := fake.NewSimpleClientset()
    
    // 部署测试Pod
    testPods := createTestPods(clientset, 10)
    
    // 创建不均衡负载
    createLoadImbalance(clientset, testPods)
    
    // 启动重调度器
    rescheduler := NewRescheduler(clientset, testConfig)
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    
    go rescheduler.Run(ctx)
    
    // 等待重调度完成
    time.Sleep(60 * time.Second)
    
    // 验证负载均衡
    assertLoadBalanced(t, clientset)
}
```

## 🚀 发布流程

### 版本发布步骤
```bash
# 1. 更新版本号
git tag v1.1.0

# 2. 构建发布镜像
make build-scheduler
docker build -t scheduler-plugins:v1.1.0 .

# 3. 运行完整测试套件
make test-all

# 4. 更新文档
# 更新 README.md 版本信息
# 更新 CHANGELOG.md

# 5. 创建发布PR
git checkout -b release/v1.1.0
git add .
git commit -m "Release v1.1.0"
git push origin release/v1.1.0
```

### 文档更新检查清单
- [ ] README.md 版本和功能更新
- [ ] 配置参数文档完整性
- [ ] 示例配置更新
- [ ] 故障排除指南更新
- [ ] API 文档更新

## 🔧 贡献指南

### 代码规范
```go
// 函数命名：驼峰命名，动词开头
func processRescheduling() {}

// 常量命名：全大写，下划线分隔
const MAX_RESCHEDULE_PODS = 10

// 错误处理：返回错误而不是panic
func doSomething() error {
    if err != nil {
        return fmt.Errorf("failed to do something: %w", err)
    }
    return nil
}

// 日志记录：使用结构化日志
klog.InfoS("重调度完成", 
    "pod", pod.Name, 
    "sourceNode", sourceNode, 
    "targetNode", targetNode,
    "reason", reason)
```

### Git提交规范
```bash
# 提交信息格式
<type>(<scope>): <description>

# 类型说明
feat:     新功能
fix:      Bug修复  
docs:     文档更新
style:    代码格式
refactor: 重构
test:     测试
chore:    构建过程或辅助工具变动

# 示例
feat(rescheduler): 添加预防性重调度功能
fix(scheduler): 修复节点资源计算错误
docs(readme): 更新部署指南
```

### Pull Request流程
1. **Fork仓库**并创建功能分支
2. **编写代码**遵循代码规范
3. **添加测试**确保覆盖率
4. **更新文档**如果需要
5. **运行测试**确保通过
6. **提交PR**描述清楚变更

### 代码审查要点
- [ ] 代码逻辑正确性
- [ ] 错误处理完整性
- [ ] 测试覆盖率充足
- [ ] 文档更新及时
- [ ] 性能影响评估
- [ ] 向后兼容性

## 🛠️ 开发工具

### 推荐工具
```bash
# 代码格式化
go fmt ./...
goimports -w .

# 代码检查
golangci-lint run

# 依赖检查
go mod tidy
go mod verify

# 安全扫描
gosec ./...
```

### IDE配置
推荐使用VSCode配置：
```json
{
    "go.formatTool": "goimports",
    "go.lintTool": "golangci-lint",
    "go.testFlags": ["-v"],
    "go.coverOnSave": true,
    "go.coverageDecorator": {
        "type": "gutter"
    }
}
```

### 调试配置
```json
{
    "name": "Debug Scheduler",
    "type": "go",
    "request": "launch",
    "mode": "debug",
    "program": "${workspaceFolder}/cmd/scheduler",
    "args": [
        "--config=${workspaceFolder}/manifests/rescheduler/config.yaml",
        "--v=4"
    ],
    "env": {
        "KUBECONFIG": "${workspaceFolder}/.kube/config"
    }
}
```

## 📊 性能优化

### 性能指标
- 调度延迟: < 100ms
- 重调度决策时间: < 5s  
- 内存使用: < 512MB
- CPU使用: < 500m (正常负载)

### 优化技巧
```go
// 使用对象池减少GC压力
var podPool = &sync.Pool{
    New: func() interface{} {
        return &v1.Pod{}
    },
}

// 批量处理减少API调用
func batchUpdatePods(pods []*v1.Pod) error {
    for _, pod := range pods {
        // 批量更新逻辑
    }
}

// 使用缓存减少重复计算
type nodeUsageCache struct {
    mu    sync.RWMutex
    cache map[string]*NodeUsage
    ttl   time.Duration
}
```

---

**相关文档**: [README](./README.md) | [部署指南](./deployment-guide.md) | [配置参考](./configuration.md)
