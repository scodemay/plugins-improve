#!/bin/bash
# 重调度器自动化测试脚本
# 用于验证调度器的各项功能

set -e

# 配置参数
NAMESPACE="default"
SCHEDULER_NAME="rescheduler-scheduler"
TEST_TIMEOUT="300"  # 5分钟超时
VERBOSE=false

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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
重调度器自动化测试脚本

用法: $0 [选项]

选项:
    -h, --help          显示帮助信息
    -v, --verbose       详细输出
    -n, --namespace     测试命名空间 (默认: default)
    -t, --timeout       测试超时时间 (默认: 300秒)
    -s, --scheduler     调度器名称 (默认: rescheduler-scheduler)

测试包括:
    1. 环境检查
    2. 调度器功能测试
    3. 过滤功能测试
    4. 评分功能测试
    5. 重调度功能测试
    6. 清理测试资源

示例:
    $0                      # 运行所有测试
    $0 -v -t 600           # 详细模式，10分钟超时
    $0 -n test-namespace   # 指定测试命名空间
EOF
}

# 解析命令行参数
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_help
            exit 0
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -t|--timeout)
            TEST_TIMEOUT="$2"
            shift 2
            ;;
        -s|--scheduler)
            SCHEDULER_NAME="$2"
            shift 2
            ;;
        *)
            log_error "未知参数: $1"
            show_help
            exit 1
            ;;
    esac
done

# 详细输出设置
if [ "$VERBOSE" = true ]; then
    set -x
fi

# 检查依赖命令
check_dependencies() {
    log_info "检查依赖命令..."
    
    local deps=("kubectl" "jq" "curl")
    for cmd in "${deps[@]}"; do
        if ! command -v "$cmd" &> /dev/null; then
            log_error "缺少依赖命令: $cmd"
            exit 1
        fi
    done
    
    log_success "依赖检查通过"
}

# 环境检查
check_environment() {
    log_info "检查Kubernetes环境..."
    
    # 检查集群连接
    if ! kubectl cluster-info &> /dev/null; then
        log_error "无法连接到Kubernetes集群"
        exit 1
    fi
    
    # 检查命名空间
    if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
        log_warning "命名空间 $NAMESPACE 不存在，创建中..."
        kubectl create namespace "$NAMESPACE"
    fi
    
    # 检查调度器是否运行
    if ! kubectl get pods -n kube-system -l app=rescheduler-scheduler | grep -q Running; then
        log_error "重调度器未在运行"
        exit 1
    fi
    
    # 检查Metrics Server
    if ! kubectl top nodes &> /dev/null; then
        log_warning "Metrics Server可能未正常工作"
    fi
    
    log_success "环境检查通过"
}

# 测试调度器基本功能
test_scheduler_basic() {
    log_info "测试调度器基本功能..."
    
    # 创建测试Pod
    kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-basic-scheduling
  namespace: $NAMESPACE
spec:
  schedulerName: $SCHEDULER_NAME
  containers:
  - name: nginx
    image: nginx:1.21
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
EOF

    # 等待Pod调度
    log_info "等待Pod调度..."
    if kubectl wait --for=condition=PodScheduled pod/test-basic-scheduling -n "$NAMESPACE" --timeout=60s; then
        log_success "基本调度功能正常"
    else
        log_error "Pod调度失败"
        kubectl describe pod test-basic-scheduling -n "$NAMESPACE"
        return 1
    fi
    
    # 清理
    kubectl delete pod test-basic-scheduling -n "$NAMESPACE" --wait=false
}

# 测试过滤功能
test_filter_function() {
    log_info "测试过滤功能..."
    
    # 创建大资源需求Pod（应该被正确过滤）
    kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-filter-high-resource
  namespace: $NAMESPACE
spec:
  schedulerName: $SCHEDULER_NAME
  containers:
  - name: stress
    image: polinux/stress
    args: ["stress", "--cpu", "1", "--timeout", "60s"]
    resources:
      requests:
        cpu: "2000m"  # 2核CPU，应该触发过滤
        memory: "2Gi"
EOF

    # 等待调度结果
    sleep 10
    
    # 检查Pod是否被正确调度到合适的节点
    local pod_node=$(kubectl get pod test-filter-high-resource -n "$NAMESPACE" -o jsonpath='{.spec.nodeName}' 2>/dev/null || echo "")
    
    if [ -n "$pod_node" ]; then
        log_success "高资源Pod成功调度到节点: $pod_node"
        
        # 检查节点资源情况
        if [ "$VERBOSE" = true ]; then
            log_info "节点资源使用情况:"
            kubectl top node "$pod_node" || true
        fi
    else
        log_warning "高资源Pod未能调度（可能因为资源不足）"
    fi
    
    # 清理
    kubectl delete pod test-filter-high-resource -n "$NAMESPACE" --wait=false
}

# 测试评分功能
test_score_function() {
    log_info "测试评分功能..."
    
    # 创建多个相同的Pod测试负载均衡
    for i in {1..5}; do
        kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-score-$i
  namespace: $NAMESPACE
spec:
  schedulerName: $SCHEDULER_NAME
  containers:
  - name: nginx
    image: nginx:1.21
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
EOF
    done
    
    # 等待所有Pod调度
    log_info "等待多个Pod调度..."
    sleep 30
    
    # 检查Pod分布
    log_info "检查Pod分布..."
    local node_distribution=$(kubectl get pods -n "$NAMESPACE" -l app!=rescheduler-test -o wide | grep test-score | awk '{print $7}' | sort | uniq -c)
    
    if [ "$VERBOSE" = true ]; then
        echo "Pod分布情况:"
        echo "$node_distribution"
    fi
    
    # 检查是否有负载均衡效果
    local unique_nodes=$(echo "$node_distribution" | wc -l)
    if [ "$unique_nodes" -gt 1 ]; then
        log_success "评分功能正常，Pod分布到 $unique_nodes 个节点"
    else
        log_warning "Pod都调度到了同一个节点，可能需要调整评分算法"
    fi
    
    # 清理
    for i in {1..5}; do
        kubectl delete pod test-score-$i -n "$NAMESPACE" --wait=false
    done
}

# 测试重调度功能
test_rescheduling_function() {
    log_info "测试重调度功能..."
    
    # 部署测试应用
    kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-rescheduling
  namespace: $NAMESPACE
spec:
  replicas: 3
  selector:
    matchLabels:
      app: test-rescheduling
  template:
    metadata:
      labels:
        app: test-rescheduling
    spec:
      schedulerName: $SCHEDULER_NAME
      containers:
      - name: nginx
        image: nginx:1.21
        resources:
          requests:
            cpu: "200m"
            memory: "256Mi"
EOF

    # 等待部署完成
    kubectl rollout status deployment/test-rescheduling -n "$NAMESPACE" --timeout=120s
    
    # 创建高负载模拟重调度场景
    kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: stress-load-generator
  namespace: $NAMESPACE
spec:
  schedulerName: $SCHEDULER_NAME
  containers:
  - name: stress
    image: polinux/stress
    args: ["stress", "--cpu", "2", "--timeout", "120s"]
    resources:
      requests:
        cpu: "1500m"
        memory: "1Gi"
EOF

    # 监控重调度事件
    log_info "监控重调度事件（60秒）..."
    timeout 60 kubectl get events -n "$NAMESPACE" --watch | grep -i evict || true
    
    # 检查是否有重调度发生
    local eviction_events=$(kubectl get events -n "$NAMESPACE" --field-selector reason=Evicted --no-headers 2>/dev/null | wc -l)
    
    if [ "$eviction_events" -gt 0 ]; then
        log_success "检测到 $eviction_events 个重调度事件"
    else
        log_warning "未检测到重调度事件（可能负载不够高）"
    fi
    
    # 清理
    kubectl delete deployment test-rescheduling -n "$NAMESPACE"
    kubectl delete pod stress-load-generator -n "$NAMESPACE" --wait=false
}

# 测试预防性重调度
test_preventive_rescheduling() {
    log_info "测试预防性重调度功能..."
    
    # 创建会触发预防性重调度的Pod
    kubectl apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: test-preventive
  namespace: $NAMESPACE
spec:
  schedulerName: $SCHEDULER_NAME
  containers:
  - name: nginx
    image: nginx:1.21
    resources:
      requests:
        cpu: "1000m"  # 较大的资源请求
        memory: "1Gi"
EOF

    # 等待Pod调度
    sleep 10
    
    # 检查PreBind日志
    log_info "检查预防性重调度日志..."
    local scheduler_logs=$(kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=50)
    
    if echo "$scheduler_logs" | grep -q "预测分析\|PreBind"; then
        log_success "预防性重调度功能正常"
    else
        log_warning "未找到预防性重调度相关日志"
    fi
    
    # 清理
    kubectl delete pod test-preventive -n "$NAMESPACE" --wait=false
}

# 性能测试
test_performance() {
    log_info "执行性能测试..."
    
    # 创建大量Pod测试调度器性能
    kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: performance-test
  namespace: $NAMESPACE
spec:
  replicas: 20
  selector:
    matchLabels:
      app: performance-test
  template:
    metadata:
      labels:
        app: performance-test
    spec:
      schedulerName: $SCHEDULER_NAME
      containers:
      - name: pause
        image: k8s.gcr.io/pause:3.9
        resources:
          requests:
            cpu: "10m"
            memory: "16Mi"
EOF

    # 测量调度时间
    log_info "测量调度性能..."
    local start_time=$(date +%s)
    
    kubectl rollout status deployment/performance-test -n "$NAMESPACE" --timeout=180s
    
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))
    
    log_success "20个Pod调度完成，耗时: ${duration}秒"
    
    # 清理
    kubectl delete deployment performance-test -n "$NAMESPACE"
}

# 收集诊断信息
collect_diagnostics() {
    log_info "收集诊断信息..."
    
    local diag_dir="rescheduler-diagnostics-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$diag_dir"
    
    # 调度器状态
    kubectl get pods -n kube-system -l app=rescheduler-scheduler -o yaml > "$diag_dir/scheduler-pods.yaml"
    kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=1000 > "$diag_dir/scheduler-logs.txt"
    
    # 节点状态
    kubectl get nodes -o yaml > "$diag_dir/nodes.yaml"
    kubectl top nodes > "$diag_dir/node-metrics.txt" 2>/dev/null || echo "Metrics not available" > "$diag_dir/node-metrics.txt"
    
    # 事件
    kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' > "$diag_dir/events.txt"
    kubectl get events -n kube-system --sort-by='.lastTimestamp' > "$diag_dir/system-events.txt"
    
    # 配置
    kubectl get configmap -n kube-system rescheduler-config -o yaml > "$diag_dir/config.yaml" 2>/dev/null || echo "Config not found" > "$diag_dir/config.yaml"
    
    log_success "诊断信息保存到: $diag_dir"
}

# 清理测试资源
cleanup_test_resources() {
    log_info "清理测试资源..."
    
    # 删除测试Pod和Deployment
    kubectl delete pods -n "$NAMESPACE" -l "app in (test-rescheduling,performance-test)" --wait=false
    kubectl delete pods -n "$NAMESPACE" --field-selector="status.phase=Failed" --wait=false
    kubectl delete pods -n "$NAMESPACE" --field-selector="status.phase=Succeeded" --wait=false
    
    # 删除测试部署
    kubectl delete deployments -n "$NAMESPACE" -l "app in (test-rescheduling,performance-test)" --wait=false
    
    log_success "测试资源清理完成"
}

# 主测试函数
run_tests() {
    log_info "开始重调度器自动化测试"
    log_info "测试参数: 命名空间=$NAMESPACE, 调度器=$SCHEDULER_NAME, 超时=$TEST_TIMEOUT秒"
    
    local start_time=$(date +%s)
    local failed_tests=0
    
    # 执行所有测试
    local tests=(
        "check_dependencies"
        "check_environment" 
        "test_scheduler_basic"
        "test_filter_function"
        "test_score_function"
        "test_preventive_rescheduling"
        "test_rescheduling_function"
        "test_performance"
    )
    
    for test in "${tests[@]}"; do
        log_info "执行测试: $test"
        if timeout "$TEST_TIMEOUT" bash -c "$test"; then
            log_success "测试通过: $test"
        else
            log_error "测试失败: $test"
            ((failed_tests++))
        fi
        echo "----------------------------------------"
    done
    
    # 收集诊断信息
    collect_diagnostics
    
    # 清理资源
    cleanup_test_resources
    
    # 测试总结
    local end_time=$(date +%s)
    local total_duration=$((end_time - start_time))
    
    echo ""
    log_info "=========================================="
    log_info "测试总结"
    log_info "=========================================="
    log_info "总测试数量: ${#tests[@]}"
    log_info "失败测试数量: $failed_tests"
    log_info "总耗时: ${total_duration}秒"
    
    if [ "$failed_tests" -eq 0 ]; then
        log_success "🎉 所有测试通过！"
        exit 0
    else
        log_error "❌ 有 $failed_tests 个测试失败"
        exit 1
    fi
}

# 脚本入口
main() {
    echo "🚀 重调度器自动化测试脚本"
    echo "作者: Kubernetes调度器开发团队"
    echo "版本: 1.0.0"
    echo ""
    
    run_tests
}

# 处理中断信号
trap cleanup_test_resources EXIT

# 执行主函数
main "$@"

