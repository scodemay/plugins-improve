#!/bin/bash

# 重调度器演示脚本
# 此脚本演示如何使用重调度功能

set -e

echo "🚀 重调度器演示脚本"
echo "===================="

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 检查依赖
check_dependencies() {
    echo -e "${BLUE}检查依赖...${NC}"
    
    if ! command -v kubectl &> /dev/null; then
        echo -e "${RED}错误: kubectl 未安装${NC}"
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        echo -e "${RED}错误: 无法连接到Kubernetes集群${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✓ 依赖检查通过${NC}"
}

# 构建调度器
build_scheduler() {
    echo -e "${BLUE}构建重调度器...${NC}"
    
    cd "$(dirname "$0")"/../../..
    
    # 编译调度器
    if make build; then
        echo -e "${GREEN}✓ 调度器构建成功${NC}"
    else
        echo -e "${RED}✗ 调度器构建失败${NC}"
        exit 1
    fi
}

# 更新配置文件
update_config() {
    echo -e "${BLUE}更新配置文件...${NC}"
    
    # 获取当前kubeconfig路径
    KUBECONFIG_PATH=${KUBECONFIG:-$HOME/.kube/config}
    
    if [ ! -f "$KUBECONFIG_PATH" ]; then
        echo -e "${RED}错误: kubeconfig文件不存在: $KUBECONFIG_PATH${NC}"
        exit 1
    fi
    
    # 更新调度器配置文件
    CONFIG_FILE="$(dirname "$0")/scheduler-config.yaml"
    if [ -f "$CONFIG_FILE" ]; then
        sed -i "s|REPLACE_ME_WITH_KUBE_CONFIG_PATH|$KUBECONFIG_PATH|g" "$CONFIG_FILE"
        echo -e "${GREEN}✓ 配置文件已更新: $CONFIG_FILE${NC}"
    else
        echo -e "${RED}错误: 配置文件不存在: $CONFIG_FILE${NC}"
        exit 1
    fi
}

# 启动重调度器
start_rescheduler() {
    echo -e "${BLUE}启动重调度器...${NC}"
    
    CONFIG_FILE="$(dirname "$0")/scheduler-config.yaml"
    SCHEDULER_BIN="$(dirname "$0")/../../../bin/kube-scheduler"
    
    if [ ! -f "$SCHEDULER_BIN" ]; then
        echo -e "${RED}错误: 调度器二进制文件不存在: $SCHEDULER_BIN${NC}"
        echo -e "${YELLOW}请先运行 'make build' 构建调度器${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}启动重调度器进程...${NC}"
    echo -e "${YELLOW}提示: 使用 Ctrl+C 停止调度器${NC}"
    echo ""
    
    # 启动调度器
    "$SCHEDULER_BIN" --config="$CONFIG_FILE" --v=2
}

# 部署测试Pod
deploy_test_pods() {
    echo -e "${BLUE}部署测试Pod...${NC}"
    
    TEST_PODS_FILE="$(dirname "$0")/test-pods.yaml"
    
    if [ ! -f "$TEST_PODS_FILE" ]; then
        echo -e "${RED}错误: 测试Pod文件不存在: $TEST_PODS_FILE${NC}"
        exit 1
    fi
    
    kubectl apply -f "$TEST_PODS_FILE"
    echo -e "${GREEN}✓ 测试Pod已部署${NC}"
    
    echo ""
    echo -e "${BLUE}等待Pod启动...${NC}"
    sleep 10
    
    echo ""
    echo -e "${BLUE}当前Pod状态:${NC}"
    kubectl get pods -l app=test-rescheduler -o wide
}

# 查看Pod分布
show_pod_distribution() {
    echo -e "${BLUE}Pod分布情况:${NC}"
    echo ""
    
    kubectl get pods -l app=test-rescheduler -o wide
    
    echo ""
    echo -e "${BLUE}节点资源使用情况:${NC}"
    kubectl top nodes 2>/dev/null || echo -e "${YELLOW}提示: metrics-server 未安装，无法显示资源使用情况${NC}"
}

# 模拟节点维护
simulate_node_maintenance() {
    echo -e "${BLUE}模拟节点维护...${NC}"
    
    # 获取第一个worker节点
    WORKER_NODE=$(kubectl get nodes --no-headers | grep -v master | grep -v control-plane | head -1 | awk '{print $1}')
    
    if [ -z "$WORKER_NODE" ]; then
        echo -e "${YELLOW}警告: 未找到worker节点${NC}"
        return
    fi
    
    echo -e "${GREEN}将节点 $WORKER_NODE 设置为维护模式${NC}"
    kubectl label node "$WORKER_NODE" scheduler.alpha.kubernetes.io/maintenance=true
    
    echo ""
    echo -e "${BLUE}等待重调度器处理...${NC}"
    sleep 60
    
    echo ""
    echo -e "${BLUE}维护模式后的Pod分布:${NC}"
    kubectl get pods -l app=test-rescheduler -o wide
    
    # 恢复节点
    echo ""
    echo -e "${GREEN}恢复节点 $WORKER_NODE${NC}"
    kubectl label node "$WORKER_NODE" scheduler.alpha.kubernetes.io/maintenance-
}

# 清理资源
cleanup() {
    echo -e "${BLUE}清理测试资源...${NC}"
    
    # 删除测试Pod
    kubectl delete pods -l app=test-rescheduler --ignore-not-found=true
    
    # 清理节点标签
    kubectl get nodes --no-headers | awk '{print $1}' | while read node; do
        kubectl label node "$node" scheduler.alpha.kubernetes.io/maintenance- 2>/dev/null || true
    done
    
    echo -e "${GREEN}✓ 清理完成${NC}"
}

# 显示帮助信息
show_help() {
    echo "重调度器演示脚本"
    echo ""
    echo "用法: $0 [命令]"
    echo ""
    echo "命令:"
    echo "  build          构建重调度器"
    echo "  start          启动重调度器 (默认)"
    echo "  test           部署测试Pod"
    echo "  status         查看Pod分布状态"
    echo "  maintenance    模拟节点维护"
    echo "  cleanup        清理测试资源"
    echo "  help           显示此帮助信息"
    echo ""
    echo "示例:"
    echo "  $0 build       # 构建调度器"
    echo "  $0 start       # 启动重调度器"
    echo "  $0 test        # 在新终端中部署测试Pod"
    echo ""
}

# 主函数
main() {
    case "${1:-start}" in
        "build")
            check_dependencies
            build_scheduler
            ;;
        "start")
            check_dependencies
            update_config
            start_rescheduler
            ;;
        "test")
            check_dependencies
            deploy_test_pods
            ;;
        "status")
            show_pod_distribution
            ;;
        "maintenance")
            simulate_node_maintenance
            ;;
        "cleanup")
            cleanup
            ;;
        "help"|"-h"|"--help")
            show_help
            ;;
        *)
            echo -e "${RED}错误: 未知命令 '$1'${NC}"
            echo ""
            show_help
            exit 1
            ;;
    esac
}

# 捕获中断信号
trap cleanup EXIT

# 运行主函数
main "$@"
