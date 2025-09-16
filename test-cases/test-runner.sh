#!/bin/bash

# Rescheduler调度性能测试脚本
# 创建500+个Pod并监控调度情况

set -e

NAMESPACE="load-test"
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${TEST_DIR}/logs"
RESULTS_DIR="${TEST_DIR}/results"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 创建必要的目录
mkdir -p "${LOG_DIR}" "${RESULTS_DIR}"

# 日志函数
log() {
    echo -e "${GREEN}[$(date '+%Y-%m-%d %H:%M:%S')] $1${NC}"
}

warn() {
    echo -e "${YELLOW}[$(date '+%Y-%m-%d %H:%M:%S')] WARNING: $1${NC}"
}

error() {
    echo -e "${RED}[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: $1${NC}"
}

info() {
    echo -e "${BLUE}[$(date '+%Y-%m-%d %H:%M:%S')] INFO: $1${NC}"
}

# 检查前置条件
check_prerequisites() {
    log "检查前置条件..."
    
    # 检查kubectl
    if ! command -v kubectl &> /dev/null; then
        error "kubectl 未安装"
        exit 1
    fi
    
    # 检查集群连接
    if ! kubectl cluster-info &> /dev/null; then
        error "无法连接到Kubernetes集群"
        exit 1
    fi
    
    # 检查节点状态
    local ready_nodes=$(kubectl get nodes --no-headers | grep Ready | wc -l)
    if [ "$ready_nodes" -lt 3 ]; then
        warn "集群只有 $ready_nodes 个Ready节点，建议至少3个节点进行测试"
    fi
    
    log "前置条件检查完成 - $ready_nodes 个Ready节点"
}

# 获取基准数据
collect_baseline_metrics() {
    log "收集基准指标数据..."
    
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local baseline_file="${RESULTS_DIR}/baseline_${timestamp}.txt"
    
    {
        echo "=== 基准指标数据 - $(date) ==="
        echo ""
        echo "节点信息:"
        kubectl get nodes -o wide
        echo ""
        echo "节点资源使用情况:"
        kubectl top nodes 2>/dev/null || echo "metrics-server不可用"
        echo ""
        echo "现有Pod分布:"
        kubectl get pods --all-namespaces -o wide | grep -v "kube-system\|monitoring" | head -20
        echo ""
        echo "调度器状态:"
        kubectl get pods -n kube-system | grep scheduler
        echo ""
        echo "Rescheduler状态:"
        kubectl get pods --all-namespaces | grep rescheduler || echo "Rescheduler未运行"
    } > "$baseline_file"
    
    log "基准数据已保存到: $baseline_file"
}

# 部署测试负载
deploy_test_load() {
    log "开始部署测试负载..."
    
    # 应用测试配置
    kubectl apply -f "${TEST_DIR}/load-test-optimized.yaml"
    
    log "等待namespace创建..."
    kubectl wait --for=condition=Active namespace/$NAMESPACE --timeout=60s
    
    log "测试负载部署完成，等待Pod启动..."
    
    # 监控部署进度
    local max_wait=420  # 7分钟超时
    local wait_time=0
    local interval=10
    
    while [ $wait_time -lt $max_wait ]; do
        local total_pods=$(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | wc -l)
        local running_pods=$(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep Running | wc -l)
        local pending_pods=$(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep Pending | wc -l)
        
        info "Pod状态: 总数=$total_pods, 运行中=$running_pods, 等待中=$pending_pods"
        
        if [ "$total_pods" -ge 500 ] && [ "$pending_pods" -eq 0 ]; then
            log "所有Pod部署完成！"
            break
        fi
        
        sleep $interval
        wait_time=$((wait_time + interval))
    done
    
    if [ $wait_time -ge $max_wait ]; then
        warn "部署超时，但继续进行测试..."
    fi
}

# 实时监控调度情况
monitor_scheduling() {
    log "开始监控调度情况..."
    
    local monitoring_duration=300  # 监控5分钟
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local monitor_file="${RESULTS_DIR}/scheduling_monitor_${timestamp}.txt"
    
    {
        echo "=== 调度监控数据 - $(date) ==="
        echo ""
        
        for i in $(seq 1 30); do  # 每10秒采集一次，持续5分钟
            echo "--- 第 $i 次采集 ($(date)) ---"
            
            # Pod分布统计
            echo "各节点Pod分布:"
            kubectl get pods -n $NAMESPACE -o wide --no-headers 2>/dev/null | \
                awk '{print $7}' | sort | uniq -c | sort -nr
            
            # 各应用类型分布
            echo ""
            echo "各应用类型Pod分布:"
            kubectl get pods -n $NAMESPACE -o wide --no-headers 2>/dev/null | \
                awk '{print $7 " " $1}' | \
                while read node pod; do
                    if [[ $pod == cpu-intensive* ]]; then
                        echo "$node CPU-INTENSIVE"
                    elif [[ $pod == memory-intensive* ]]; then
                        echo "$node MEMORY-INTENSIVE"
                    elif [[ $pod == balanced-load* ]]; then
                        echo "$node BALANCED"
                    elif [[ $pod == lightweight* ]]; then
                        echo "$node LIGHTWEIGHT"
                    fi
                done | sort | uniq -c
            
            # 资源使用情况
            echo ""
            echo "节点资源使用:"
            kubectl top nodes 2>/dev/null || echo "metrics-server不可用"
            
            # Pod状态统计
            echo ""
            echo "Pod状态统计:"
            kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | \
                awk '{print $3}' | sort | uniq -c
            
            echo ""
            echo "================================"
            echo ""
            
            sleep 10
        done
    } > "$monitor_file" &
    
    local monitor_pid=$!
    log "后台监控进程启动 (PID: $monitor_pid)，数据将保存到: $monitor_file"
    
    # 返回监控进程PID供后续使用
    echo $monitor_pid
}

# 生成调度分析报告
generate_report() {
    log "生成调度分析报告..."
    
    local timestamp=$(date +%Y%m%d_%H%M%S)
    local report_file="${RESULTS_DIR}/scheduling_report_${timestamp}.txt"
    
    {
        echo "======================================"
        echo "   Rescheduler 调度性能测试报告"
        echo "======================================"
        echo "测试时间: $(date)"
        echo "测试配置: 500+ Pods, 4种工作负载类型"
        echo ""
        
        # 集群基本信息
        echo "1. 集群信息"
        echo "----------"
        echo "节点总数: $(kubectl get nodes --no-headers | wc -l)"
        echo "可调度节点: $(kubectl get nodes --no-headers | grep Ready | wc -l)"
        echo ""
        kubectl get nodes -o custom-columns=NAME:.metadata.name,STATUS:.status.conditions[-1].type,ROLES:.metadata.labels.'node-role\.kubernetes\.io/.*',AGE:.metadata.creationTimestamp
        echo ""
        
        # Pod分布分析
        echo "2. Pod分布分析"
        echo "-------------"
        echo "总Pod数: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | wc -l)"
        echo ""
        
        echo "各节点Pod分布:"
        kubectl get pods -n $NAMESPACE -o wide --no-headers 2>/dev/null | \
            awk '{node[$7]++} END {for (n in node) printf "%-30s %d\n", n, node[n]}' | sort -k2 -nr
        echo ""
        
        echo "各应用类型分布:"
        echo "CPU密集型: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep cpu-intensive | wc -l)"
        echo "内存密集型: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep memory-intensive | wc -l)"
        echo "均衡负载: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep balanced-load | wc -l)"
        echo "轻量级: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep lightweight | wc -l)"
        echo ""
        
        # 调度均匀性分析
        echo "3. 调度均匀性分析"
        echo "---------------"
        
        # 计算节点间Pod数量方差
        local pod_counts=($(kubectl get pods -n $NAMESPACE -o wide --no-headers 2>/dev/null | \
            awk '{node[$7]++} END {for (n in node) print node[n]}'))
        
        if [ ${#pod_counts[@]} -gt 0 ]; then
            local sum=0
            local count=${#pod_counts[@]}
            
            # 计算平均值
            for pc in "${pod_counts[@]}"; do
                sum=$((sum + pc))
            done
            local avg=$((sum / count))
            
            # 计算方差
            local variance_sum=0
            for pc in "${pod_counts[@]}"; do
                local diff=$((pc - avg))
                variance_sum=$((variance_sum + diff * diff))
            done
            local variance=$((variance_sum / count))
            
            echo "平均每节点Pod数: $avg"
            echo "Pod分布方差: $variance"
            echo "分布标准差: $(echo "sqrt($variance)" | bc -l 2>/dev/null || echo "计算失败")"
            
            # 负载均衡评分 (方差越小越好)
            if [ $variance -lt 10 ]; then
                echo "负载均衡评分: 优秀 (方差 < 10)"
            elif [ $variance -lt 25 ]; then
                echo "负载均衡评分: 良好 (方差 < 25)"
            elif [ $variance -lt 50 ]; then
                echo "负载均衡评分: 一般 (方差 < 50)"
            else
                echo "负载均衡评分: 需要改进 (方差 >= 50)"
            fi
        fi
        echo ""
        
        # 资源使用情况
        echo "4. 资源使用情况"
        echo "-------------"
        kubectl top nodes 2>/dev/null || echo "metrics-server不可用，无法获取资源使用数据"
        echo ""
        
        # 失败Pod分析
        echo "5. 失败Pod分析"
        echo "-------------"
        local failed_pods=$(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep -v Running | grep -v Completed)
        if [ -n "$failed_pods" ]; then
            echo "非Running状态的Pod:"
            echo "$failed_pods"
        else
            echo "所有Pod状态正常"
        fi
        echo ""
        
        # 调度事件分析
        echo "6. 调度事件分析"
        echo "-------------"
        echo "最近的调度相关事件:"
        kubectl get events -n $NAMESPACE --sort-by='.lastTimestamp' 2>/dev/null | tail -10
        echo ""
        
        echo "======================================"
        echo "报告生成完成: $(date)"
        echo "======================================"
        
    } > "$report_file"
    
    log "调度分析报告已生成: $report_file"
    
    # 显示关键信息
    echo ""
    info "=== 关键测试结果 ==="
    echo "总Pod数: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | wc -l)"
    echo "运行中Pod数: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep Running | wc -l)"
    echo "失败Pod数: $(kubectl get pods -n $NAMESPACE --no-headers 2>/dev/null | grep -v Running | grep -v Completed | wc -l)"
    echo ""
    echo "各节点Pod分布:"
    kubectl get pods -n $NAMESPACE -o wide --no-headers 2>/dev/null | \
        awk '{node[$7]++} END {for (n in node) printf "  %-30s %d pods\n", n, node[n]}' | sort -k2 -nr
}

# 清理测试环境
cleanup() {
    log "清理测试环境..."
    
    read -p "是否删除测试namespace '$NAMESPACE' 和所有测试Pod? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        kubectl delete namespace $NAMESPACE --ignore-not-found=true
        log "测试环境清理完成"
    else
        warn "测试环境保留，请手动清理: kubectl delete namespace $NAMESPACE"
    fi
}

# 显示帮助信息
show_help() {
    echo "Rescheduler调度性能测试脚本"
    echo ""
    echo "用法: $0 [选项]"
    echo ""
    echo "选项:"
    echo "  --deploy-only    只部署测试负载，不进行监控"
    echo "  --monitor-only   只监控现有Pod，不部署新负载"
    echo "  --cleanup        清理测试环境"
    echo "  --help          显示此帮助信息"
    echo ""
    echo "默认行为: 执行完整的测试流程（部署 + 监控 + 报告）"
}

# 主函数
main() {
    case "$1" in
        --deploy-only)
            check_prerequisites
            collect_baseline_metrics
            deploy_test_load
            log "部署完成，使用 --monitor-only 开始监控"
            ;;
        --monitor-only)
            check_prerequisites
            if ! kubectl get namespace $NAMESPACE &>/dev/null; then
                error "测试namespace '$NAMESPACE' 不存在，请先运行 --deploy-only"
                exit 1
            fi
            local monitor_pid=$(monitor_scheduling)
            sleep 300  # 等待监控完成
            generate_report
            ;;
        --cleanup)
            cleanup
            ;;
        --help)
            show_help
            ;;
        "")
            # 完整测试流程
            log "开始Rescheduler调度性能测试..."
            check_prerequisites
            collect_baseline_metrics
            deploy_test_load
            
            log "等待Pod稳定运行..."
            sleep 30
            
            local monitor_pid=$(monitor_scheduling)
            
            log "监控中，请等待5分钟..."
            sleep 300
            
            # 确保监控进程结束
            kill $monitor_pid 2>/dev/null || true
            wait $monitor_pid 2>/dev/null || true
            
            generate_report
            
            echo ""
            log "测试完成！查看结果目录: $RESULTS_DIR"
            cleanup
            ;;
        *)
            error "未知选项: $1"
            show_help
            exit 1
            ;;
    esac
}

# 信号处理
trap 'error "测试被中断"; cleanup; exit 1' INT TERM

# 执行主函数
main "$@"
