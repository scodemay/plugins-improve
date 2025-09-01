# 📁 重调度器项目结构

## 🗂️ 核心目录结构

```
scheduler-plugins/
├── 📚 文档
│   ├── README.md                          # 项目总览
│   ├── 重调度器部署指南.md                 # 完整部署文档 
│   ├── 快速启动.md                        # 快速启动指南
│   └── PROJECT-STRUCTURE.md               # 本文件
│
├── 🔧 构建和部署
│   ├── Makefile                           # 主构建文件
│   ├── Makefile.simple                    # 简化构建文件
│   └── Dockerfile.local                   # 镜像构建文件
│
├── 📦 重调度器核心代码
│   ├── pkg/rescheduler/
│   │   ├── rescheduler.go                 # 重调度器主逻辑
│   │   ├── controller.go                  # 迁移控制器
│   │   └── README.md                      # 详细技术文档
│   │
│   └── cmd/scheduler/
│       └── main.go                        # 调度器入口点
│
├── 🚀 部署配置
│   ├── manifests/rescheduler/
│   │   ├── deployment.yaml                # 完整部署配置
│   │   ├── quick-test.yaml                # 快速测试配置
│   │   └── test-pods.yaml                 # 完整测试场景
│   │
│   └── manifests/Tinyscheduler/           # 其他调度器示例
│       ├── scheduler-config.yaml
│       └── test-pod*.yaml
│
└── 📋 其他文件
    ├── go.mod/go.sum                      # Go依赖管理
    ├── apis/                              # API定义
    ├── build/                             # 构建脚本
    └── vendor/                            # 依赖包
```

## 🎯 关键文件说明

### 📖 文档文件
- **重调度器部署指南.md**: 🌟 完整的部署和使用文档
- **快速启动.md**: ⚡ 5分钟快速上手指南
- **pkg/rescheduler/README.md**: 🔍 技术实现详解

### 🛠️ 构建工具
- **Makefile.simple**: ⭐ 推荐使用的简化构建工具
- **Dockerfile.local**: 📦 镜像构建配置

### 📋 部署配置
- **manifests/rescheduler/deployment.yaml**: 🎯 核心部署文件
- **manifests/rescheduler/quick-test.yaml**: 🧪 快速测试配置

## 🚀 快速开始

```bash
# 1. 一键构建和部署
make -f Makefile.simple all

# 2. 运行测试
make -f Makefile.simple test

# 3. 查看状态
make -f Makefile.simple status
```

## 🔍 深入了解

- 📖 完整指南: `重调度器部署指南.md`
- ⚡ 快速开始: `快速启动.md`  
- 🔧 技术细节: `pkg/rescheduler/README.md`

