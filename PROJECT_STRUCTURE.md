# 📁 Scheduler-Plugins 项目文件结构

本文档描述了项目的目录结构和文件组织方式。

## 📋 根目录结构

```
scheduler-plugins/
├── 📁 apis/                     # API定义
├── 📁 build/                    # 构建输出
├── 📁 cmd/                      # 主程序入口
├── 📁 config/                   # Kubernetes配置
├── 📁 docs/                     # 项目文档
│   ├── 📁 monitoring/           # 监控相关文档
│   ├── 📁 testing/              # 测试相关文档
│   └── 📁 guides/               # 使用指南
├── 📁 hack/                     # 构建和开发脚本
├── 📁 kep/                      # Kubernetes Enhancement Proposals
├── 📁 manifests/                # Kubernetes资源清单
├── 📁 monitoring/               # 监控配置文件
│   ├── 📁 configs/              # 配置文件
│   ├── 📁 deployments/          # 部署文件
│   ├── 📁 docs/                 # 监控文档
│   └── 📁 exporters/            # 指标导出器
├── 📁 pkg/                      # Go包源码
├── 📁 scripts/                  # 性能分析脚本
├── 📁 site/                     # 项目网站
├── 📁 test/                     # 单元测试
├── 📁 test-cases/               # 测试用例
├── 📁 tools/                    # 项目工具
│   ├── 📁 monitoring/           # 监控工具
│   ├── 📁 testing/              # 测试工具
│   └── 📁 deployment/           # 部署工具
├── 📄 go.mod                    # Go模块定义
├── 📄 go.sum                    # Go依赖版本锁定
├── 📄 Makefile                  # 构建规则
└── 📄 README.md                 # 项目说明
```

## 🎯 目录功能说明

### 核心代码
- **`cmd/`**: 可执行程序入口点
- **`pkg/`**: 核心Go包和库
- **`apis/`**: API定义和生成代码

### 配置和部署
- **`manifests/`**: Kubernetes部署清单
- **`config/`**: 应用配置文件
- **`monitoring/`**: 监控系统配置

### 工具和脚本
- **`tools/`**: 项目专用工具集合
  - `monitoring/`: 监控部署和管理工具
  - `testing/`: 测试执行和报告工具
  - `deployment/`: 部署自动化工具
- **`scripts/`**: 性能分析和计算脚本
- **`hack/`**: 开发和构建辅助脚本

### 文档和测试
- **`docs/`**: 项目文档
  - `monitoring/`: 监控系统文档
  - `testing/`: 测试相关文档
  - `guides/`: 用户指南
- **`test/`**: 单元测试代码
- **`test-cases/`**: 集成测试用例

### 项目管理
- **`kep/`**: Kubernetes增强提案
- **`site/`**: 项目官方网站
- **`.github/`**: GitHub配置

## 🚀 快速开始

### 监控系统
```bash
# 部署监控系统
./tools/monitoring/deploy-enhanced-monitoring.sh

# 测试监控功能
./tools/monitoring/test-separated-metrics.sh

# 访问监控界面
./tools/monitoring/access-monitoring.sh
```

### 性能测试
```bash
# 运行性能测试
./scripts/run-performance-tests.sh

# 查看测试报告
cat tools/testing/project-test-report.md
```

### 负载均衡分析
```bash
# 计算负载均衡指标
./scripts/calculate-balance.sh

# 监控性能数据
./scripts/monitor-performance.sh
```

## 📚 相关文档

- [监控系统查询指南](docs/monitoring/separated-load-balance-queries.md)
- [实施总结报告](docs/monitoring/separation-implementation-summary.md)
- [完整测试文档](tools/testing/完整测试文档.md)
- [项目测试报告](tools/testing/project-test-report.md)
