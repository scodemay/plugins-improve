#!/bin/bash
# 重调度器一键部署脚本
# 支持完整部署、升级、卸载等操作

set -e

# 默认配置
NAMESPACE="kube-system"
CONFIG_TYPE="default"
IMAGE_TAG="latest"
ENABLE_MONITORING=false
ENABLE_TESTING=false
DRY_RUN=false
VERBOSE=false

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 日志函数
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 帮助信息
show_help() {
    cat << EOF
重调度器部署脚本

用法: $0 [命令] [选项]

命令:
    install     安装重调度器
    upgrade     升级重调度器
    uninstall   卸载重调度器
    status      查看状态
    logs        查看日志
    test        运行测试

选项:
    -h, --help              显示帮助信息
    -n, --namespace         部署命名空间 (默认: kube-system)
    -c, --config            配置类型 (默认: default)
                           可选: default|production|development|hpc|memory|microservices
    -t, --tag               镜像标签 (默认: latest)
    -m, --monitoring        启用监控组件
    -T, --testing           启用测试组件
    -d, --dry-run          仅显示将要执行的操作
    -v, --verbose          详细输出

配置类型说明:
    default       标准配置，适合大多数场景
    production    生产环境保守配置
    development   开发环境激进配置
    hpc           高性能计算优化配置
    memory        内存密集型优化配置
    microservices 微服务架构配置

示例:
    $0 install                          # 标准安装
    $0 install -c production -m         # 生产环境安装并启用监控
    $0 upgrade -t v1.2.0               # 升级到指定版本
    $0 uninstall                       # 卸载
    $0 status                          # 查看状态
    $0 test                            # 运行测试

EOF
}

# 解析命令行参数
parse_args() {
    COMMAND=""
    
    while [[ $# -gt 0 ]]; do
        case $1 in
            install|upgrade|uninstall|status|logs|test)
                COMMAND="$1"
                shift
                ;;
            -h|--help)
                show_help
                exit 0
                ;;
            -n|--namespace)
                NAMESPACE="$2"
                shift 2
                ;;
            -c|--config)
                CONFIG_TYPE="$2"
                shift 2
                ;;
            -t|--tag)
                IMAGE_TAG="$2"
                shift 2
                ;;
            -m|--monitoring)
                ENABLE_MONITORING=true
                shift
                ;;
            -T|--testing)
                ENABLE_TESTING=true
                shift
                ;;
            -d|--dry-run)
                DRY_RUN=true
                shift
                ;;
            -v|--verbose)
                VERBOSE=true
                shift
                ;;
            *)
                log_error "未知参数: $1"
                show_help
                exit 1
                ;;
        esac
    done
    
    if [ -z "$COMMAND" ]; then
        log_error "必须指定命令"
        show_help
        exit 1
    fi
}

# 详细输出设置
set_verbose() {
    if [ "$VERBOSE" = true ]; then
        set -x
    fi
}

# 执行kubectl命令
run_kubectl() {
    if [ "$DRY_RUN" = true ]; then
        echo "DRY-RUN: kubectl $*"
    else
        kubectl "$@"
    fi
}

# 检查依赖
check_dependencies() {
    log_info "检查依赖..."
    
    local deps=("kubectl" "jq")
    for cmd in "${deps[@]}"; do
        if ! command -v "$cmd" &> /dev/null; then
            log_error "缺少依赖命令: $cmd"
            exit 1
        fi
    done
    
    # 检查集群连接
    if ! kubectl cluster-info &> /dev/null; then
        log_error "无法连接到Kubernetes集群"
        exit 1
    fi
    
    log_success "依赖检查通过"
}

# 检查当前状态
check_current_status() {
    log_info "检查当前部署状态..."
    
    # 检查RBAC
    if kubectl get clusterrole rescheduler-scheduler &> /dev/null; then
        echo "  ✓ RBAC已部署"
    else
        echo "  ✗ RBAC未部署"
    fi
    
    # 检查配置
    if kubectl get configmap -n "$NAMESPACE" rescheduler-config &> /dev/null; then
        echo "  ✓ 配置已部署"
    else
        echo "  ✗ 配置未部署"
    fi
    
    # 检查调度器
    if kubectl get deployment -n "$NAMESPACE" rescheduler-scheduler &> /dev/null; then
        local replicas=$(kubectl get deployment -n "$NAMESPACE" rescheduler-scheduler -o jsonpath='{.status.readyReplicas}')
        echo "  ✓ 调度器已部署 (就绪副本: ${replicas:-0})"
    else
        echo "  ✗ 调度器未部署"
    fi
    
    # 检查监控
    if kubectl get servicemonitor -n "$NAMESPACE" rescheduler-scheduler-metrics &> /dev/null; then
        echo "  ✓ 监控已部署"
    else
        echo "  ✗ 监控未部署"
    fi
}

# 获取配置文件路径
get_config_path() {
    local base_dir="$(dirname "$0")/.."
    
    case "$CONFIG_TYPE" in
        default)
            echo "$base_dir/config.yaml"
            ;;
        production)
            echo "$base_dir/examples/configuration-examples.yaml"
            ;;
        development)
            echo "$base_dir/examples/configuration-examples.yaml"
            ;;
        hpc)
            echo "$base_dir/examples/configuration-examples.yaml"
            ;;
        memory)
            echo "$base_dir/examples/configuration-examples.yaml"
            ;;
        microservices)
            echo "$base_dir/examples/configuration-examples.yaml"
            ;;
        *)
            log_error "未知配置类型: $CONFIG_TYPE"
            exit 1
            ;;
    esac
}

# 部署RBAC
deploy_rbac() {
    log_info "部署RBAC..."
    
    local base_dir="$(dirname "$0")/.."
    run_kubectl apply -f "$base_dir/rbac.yaml"
    
    log_success "RBAC部署完成"
}

# 部署配置
deploy_config() {
    log_info "部署配置 (类型: $CONFIG_TYPE)..."
    
    local config_path=$(get_config_path)
    
    if [ "$CONFIG_TYPE" = "default" ]; then
        run_kubectl apply -f "$config_path"
    else
        # 对于示例配置，需要特殊处理
        local config_name="rescheduler-config-$CONFIG_TYPE"
        
        if [ "$DRY_RUN" = true ]; then
            echo "DRY-RUN: 将从 $config_path 提取配置 $config_name"
        else
            # 提取特定配置并重命名
            kubectl get -f "$config_path" configmap "$config_name" -o yaml | \
                sed "s/$config_name/rescheduler-config/" | \
                run_kubectl apply -f -
        fi
    fi
    
    log_success "配置部署完成"
}

# 部署调度器
deploy_scheduler() {
    log_info "部署调度器 (镜像标签: $IMAGE_TAG)..."
    
    local base_dir="$(dirname "$0")/.."
    local scheduler_yaml="$base_dir/scheduler.yaml"
    
    if [ "$IMAGE_TAG" != "latest" ]; then
        # 更新镜像标签
        if [ "$DRY_RUN" = true ]; then
            echo "DRY-RUN: 将镜像标签更新为 $IMAGE_TAG"
        else
            sed "s/scheduler-plugins:latest/scheduler-plugins:$IMAGE_TAG/" "$scheduler_yaml" | \
                run_kubectl apply -f -
        fi
    else
        run_kubectl apply -f "$scheduler_yaml"
    fi
    
    log_success "调度器部署完成"
}

# 部署监控组件
deploy_monitoring() {
    if [ "$ENABLE_MONITORING" = true ]; then
        log_info "部署监控组件..."
        
        local base_dir="$(dirname "$0")"
        run_kubectl apply -f "$base_dir/monitoring.yaml"
        
        log_success "监控组件部署完成"
    fi
}

# 部署测试组件
deploy_testing() {
    if [ "$ENABLE_TESTING" = true ]; then
        log_info "部署测试组件..."
        
        local base_dir="$(dirname "$0")"
        run_kubectl apply -f "$base_dir/quick-test.yaml"
        
        log_success "测试组件部署完成"
    fi
}

# 等待部署完成
wait_for_deployment() {
    if [ "$DRY_RUN" = true ]; then
        echo "DRY-RUN: 跳过等待部署完成"
        return
    fi
    
    log_info "等待调度器就绪..."
    
    if kubectl wait --for=condition=available deployment/rescheduler-scheduler -n "$NAMESPACE" --timeout=300s; then
        log_success "调度器已就绪"
    else
        log_error "调度器部署超时"
        
        log_info "调度器Pod状态:"
        kubectl get pods -n "$NAMESPACE" -l app=rescheduler-scheduler
        
        log_info "调度器日志:"
        kubectl logs -n "$NAMESPACE" -l app=rescheduler-scheduler --tail=50
        
        exit 1
    fi
}

# 安装命令
cmd_install() {
    log_info "开始安装重调度器..."
    log_info "配置: 命名空间=$NAMESPACE, 配置类型=$CONFIG_TYPE, 镜像标签=$IMAGE_TAG"
    
    check_dependencies
    
    # 检查是否已安装
    if kubectl get deployment -n "$NAMESPACE" rescheduler-scheduler &> /dev/null; then
        log_warning "重调度器已安装，使用 'upgrade' 命令进行升级"
        exit 1
    fi
    
    # 部署组件
    deploy_rbac
    deploy_config
    deploy_scheduler
    deploy_monitoring
    deploy_testing
    
    # 等待部署完成
    wait_for_deployment
    
    log_success "🎉 重调度器安装完成！"
    
    # 显示状态
    cmd_status
}

# 升级命令
cmd_upgrade() {
    log_info "开始升级重调度器..."
    
    check_dependencies
    
    # 检查是否已安装
    if ! kubectl get deployment -n "$NAMESPACE" rescheduler-scheduler &> /dev/null; then
        log_error "重调度器未安装，使用 'install' 命令进行安装"
        exit 1
    fi
    
    # 升级组件
    deploy_config
    deploy_scheduler
    deploy_monitoring
    
    # 等待升级完成
    wait_for_deployment
    
    log_success "🎉 重调度器升级完成！"
    
    # 显示状态
    cmd_status
}

# 卸载命令
cmd_uninstall() {
    log_info "开始卸载重调度器..."
    
    if [ "$DRY_RUN" = true ]; then
        echo "DRY-RUN: 将执行卸载操作"
        return
    fi
    
    # 删除测试组件
    if [ "$ENABLE_TESTING" = true ]; then
        log_info "删除测试组件..."
        kubectl delete -f "$(dirname "$0")/quick-test.yaml" --ignore-not-found=true
    fi
    
    # 删除监控组件
    if [ "$ENABLE_MONITORING" = true ]; then
        log_info "删除监控组件..."
        kubectl delete -f "$(dirname "$0")/monitoring.yaml" --ignore-not-found=true
    fi
    
    # 删除调度器
    log_info "删除调度器..."
    local base_dir="$(dirname "$0")/.."
    kubectl delete -f "$base_dir/scheduler.yaml" --ignore-not-found=true
    
    # 删除配置
    log_info "删除配置..."
    kubectl delete configmap -n "$NAMESPACE" rescheduler-config --ignore-not-found=true
    
    # 删除RBAC
    log_info "删除RBAC..."
    kubectl delete -f "$base_dir/rbac.yaml" --ignore-not-found=true
    
    log_success "🎉 重调度器卸载完成！"
}

# 状态命令
cmd_status() {
    log_info "重调度器状态:"
    check_current_status
    
    echo ""
    log_info "调度器Pod详情:"
    kubectl get pods -n "$NAMESPACE" -l app=rescheduler-scheduler -o wide
    
    echo ""
    log_info "最近事件:"
    kubectl get events -n "$NAMESPACE" --field-selector involvedObject.name=rescheduler-scheduler --sort-by='.lastTimestamp' | tail -5
}

# 日志命令
cmd_logs() {
    log_info "显示调度器日志:"
    kubectl logs -n "$NAMESPACE" -l app=rescheduler-scheduler --tail=100 -f
}

# 测试命令
cmd_test() {
    log_info "运行自动化测试..."
    
    local base_dir="$(dirname "$0")"
    bash "$base_dir/automated-test.sh" -n "$NAMESPACE"
}

# 主函数
main() {
    echo "🚀 重调度器部署脚本"
    echo "版本: 1.0.0"
    echo ""
    
    parse_args "$@"
    set_verbose
    
    case "$COMMAND" in
        install)
            cmd_install
            ;;
        upgrade)
            cmd_upgrade
            ;;
        uninstall)
            cmd_uninstall
            ;;
        status)
            cmd_status
            ;;
        logs)
            cmd_logs
            ;;
        test)
            cmd_test
            ;;
        *)
            log_error "未知命令: $COMMAND"
            show_help
            exit 1
            ;;
    esac
}

# 执行主函数
main "$@"
