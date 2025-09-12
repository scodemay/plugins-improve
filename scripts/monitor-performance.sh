#!/bin/bash
# Rescheduler 性能监控脚本

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 配置
NAMESPACE="perf-test"
SCHEDULER_NAMESPACE="kube-system"
SCHEDULER_LABEL="app=rescheduler-scheduler"
MONITOR_INTERVAL=30

echo -e "${BLUE}=== Rescheduler 性能监控启动 ===${NC}"
echo "监控间隔: ${MONITOR_INTERVAL}s"
echo "测试命名空间: ${NAMESPACE}"
echo "调度器命名空间: ${SCHEDULER_NAMESPACE}"
echo ""

# 创建日志目录
mkdir -p logs
LOG_FILE="logs/performance-$(date +%Y%m%d-%H%M%S).log"

# 日志函数
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S'): $1" | tee -a "$LOG_FILE"
}

# 获取节点列表
get_nodes() {
    kubectl get nodes --no-headers -o custom-columns=NAME:.metadata.name | grep -v master || true
}

# 计算负载均衡度
calculate_balance() {
    echo -e "${YELLOW}## 计算节点负载均衡度${NC}"
    
    # 获取节点 CPU 使用率
    cpu_data=$(kubectl top nodes --no-headers 2>/dev/null | awk '{print $3}' | sed 's/%//' | tr '\n' ' ')
    
    if [ -z "$cpu_data" ]; then
        echo "无法获取节点 CPU 使用率数据"
        return
    fi
    
    # 使用 awk 计算统计数据
    stats=$(echo "$cpu_data" | awk '{
        sum = 0; 
        count = 0;
        for(i=1; i<=NF; i++) {
            if($i != "") {
                values[count++] = $i;
                sum += $i;
            }
        }
        if(count == 0) exit 1;
        
        avg = sum / count;
        
        # 计算方差
        variance = 0;
        for(i=0; i<count; i++) {
            variance += (values[i] - avg) ^ 2;
        }
        variance = variance / count;
        
        # 计算标准差
        std_dev = sqrt(variance);
        
        # 负载均衡度 (100% - 标准差百分比)
        balance_score = 100 - std_dev;
        if(balance_score < 0) balance_score = 0;
        
        printf "平均CPU使用率: %.2f%%\n", avg;
        printf "标准差: %.2f%%\n", std_dev;
        printf "负载均衡度: %.2f%%\n", balance_score;
        printf "节点数量: %d\n", count;
    }')
    
    echo "$stats" | tee -a "$LOG_FILE"
}

# 获取调度器状态
check_scheduler_status() {
    echo -e "${YELLOW}## 调度器状态检查${NC}"
    
    # 检查调度器 Pod 状态
    scheduler_status=$(kubectl get pods -n "$SCHEDULER_NAMESPACE" -l "$SCHEDULER_LABEL" --no-headers 2>/dev/null | awk '{print $3}')
    
    if [ "$scheduler_status" = "Running" ]; then
        echo -e "${GREEN}✓ 调度器状态: Running${NC}"
        log "调度器状态: Running"
    else
        echo -e "${RED}✗ 调度器状态: $scheduler_status${NC}"
        log "调度器状态异常: $scheduler_status"
    fi
    
    # 检查调度器资源使用
    scheduler_resources=$(kubectl top pods -n "$SCHEDULER_NAMESPACE" -l "$SCHEDULER_LABEL" --no-headers 2>/dev/null | awk '{print "CPU: "$2", Memory: "$3}')
    if [ -n "$scheduler_resources" ]; then
        echo "调度器资源使用: $scheduler_resources"
        log "调度器资源使用: $scheduler_resources"
    fi
}

# 获取 Pod 分布统计
get_pod_distribution() {
    echo -e "${YELLOW}## Pod 分布统计${NC}"
    
    # 整体 Pod 分布
    echo "### 所有 Pod 分布:"
    kubectl get pods -A -o wide --no-headers 2>/dev/null | \
        awk '{print $8}' | grep -v '<none>' | sort | uniq -c | \
        awk '{printf "  %s: %d pods\n", $2, $1}' | tee -a "$LOG_FILE"
    
    # 测试 Pod 分布
    if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
        echo "### 测试 Pod 分布 (namespace: $NAMESPACE):"
        kubectl get pods -n "$NAMESPACE" -o wide --no-headers 2>/dev/null | \
            awk '{print $7}' | grep -v '<none>' | sort | uniq -c | \
            awk '{printf "  %s: %d pods\n", $2, $1}' | tee -a "$LOG_FILE"
    fi
}

# 获取最近的调度事件
get_recent_events() {
    echo -e "${YELLOW}## 最近的调度事件${NC}"
    
    # 获取最近的调度相关事件
    recent_events=$(kubectl get events --sort-by=.metadata.creationTimestamp 2>/dev/null | \
        grep -E "(Scheduled|FailedScheduling|Evicted)" | tail -10)
    
    if [ -n "$recent_events" ]; then
        echo "$recent_events" | while read -r line; do
            echo "  $line"
            log "事件: $line"
        done
    else
        echo "  暂无最近的调度事件"
    fi
}

# 检查重调度活动
check_rescheduling_activity() {
    echo -e "${YELLOW}## 重调度活动检查${NC}"
    
    # 检查调度器日志中的重调度活动
    rescheduling_logs=$(kubectl logs -n "$SCHEDULER_NAMESPACE" -l "$SCHEDULER_LABEL" --tail=50 2>/dev/null | \
        grep -E "(重调度|负载均衡|资源优化|节点过载)" | tail -5)
    
    if [ -n "$rescheduling_logs" ]; then
        echo "### 最近的重调度活动:"
        echo "$rescheduling_logs" | while read -r line; do
            echo "  $line"
            log "重调度活动: $line"
        done
    else
        echo "  暂无重调度活动记录"
    fi
}

# 性能指标收集
collect_performance_metrics() {
    echo -e "${BLUE}=== $(date) 性能数据收集 ===${NC}"
    log "开始性能数据收集"
    
    # 1. 节点资源使用率
    echo -e "${YELLOW}## 节点资源使用率${NC}"
    kubectl top nodes 2>/dev/null | tee -a "$LOG_FILE" || echo "无法获取节点资源数据"
    
    # 2. 负载均衡度计算
    calculate_balance
    
    # 3. 调度器状态
    check_scheduler_status
    
    # 4. Pod 分布统计
    get_pod_distribution
    
    # 5. 最近事件
    get_recent_events
    
    # 6. 重调度活动
    check_rescheduling_activity
    
    echo -e "${BLUE}=== 数据收集完成 ===${NC}"
    echo ""
}

# 生成性能报告
generate_report() {
    echo -e "${GREEN}=== 生成性能报告 ===${NC}"
    
    REPORT_FILE="logs/performance-report-$(date +%Y%m%d-%H%M%S).md"
    
    cat > "$REPORT_FILE" << EOF
# Rescheduler 性能测试报告

**生成时间**: $(date)
**测试持续时间**: $1 分钟
**日志文件**: $LOG_FILE

## 测试环境
- Kubernetes 集群节点数: $(kubectl get nodes --no-headers | wc -l)
- 测试命名空间: $NAMESPACE
- 调度器版本: rescheduler-scheduler

## 关键指标摘要

### 节点负载情况
\`\`\`
$(kubectl top nodes 2>/dev/null || echo "无法获取数据")
\`\`\`

### Pod 分布统计
\`\`\`
$(kubectl get pods -n "$NAMESPACE" -o wide --no-headers 2>/dev/null | awk '{print $7}' | sort | uniq -c || echo "无法获取数据")
\`\`\`

### 调度器状态
- 状态: $(kubectl get pods -n "$SCHEDULER_NAMESPACE" -l "$SCHEDULER_LABEL" --no-headers | awk '{print $3}')
- 资源使用: $(kubectl top pods -n "$SCHEDULER_NAMESPACE" -l "$SCHEDULER_LABEL" --no-headers 2>/dev/null | awk '{print "CPU: "$2", Memory: "$3}' || echo "无法获取")

## 详细日志
详细的监控数据请查看: $LOG_FILE

## 建议
1. 如果负载均衡度低于 80%，考虑调整调度策略
2. 如果调度器资源使用过高，考虑优化配置
3. 监控重调度频率，避免过于频繁的 Pod 迁移

EOF

    echo "性能报告已生成: $REPORT_FILE"
    log "性能报告生成: $REPORT_FILE"
}

# 主监控循环
main() {
    local duration=${1:-10}  # 默认监控 10 分钟
    local cycles=$((duration * 60 / MONITOR_INTERVAL))
    
    echo "开始监控，持续时间: ${duration} 分钟 (${cycles} 个周期)"
    log "监控开始，持续时间: ${duration} 分钟"
    
    for i in $(seq 1 $cycles); do
        echo -e "${BLUE}=== 监控周期 $i/$cycles ===${NC}"
        collect_performance_metrics
        
        if [ $i -lt $cycles ]; then
            echo "等待 ${MONITOR_INTERVAL} 秒..."
            sleep $MONITOR_INTERVAL
        fi
    done
    
    generate_report $duration
    echo -e "${GREEN}监控完成！${NC}"
}

# 信号处理
trap 'echo -e "\n${YELLOW}监控被中断${NC}"; generate_report "interrupted"; exit 0' INT TERM

# 检查依赖
if ! command -v kubectl >/dev/null 2>&1; then
    echo -e "${RED}错误: kubectl 未安装或不在 PATH 中${NC}"
    exit 1
fi

# 运行主程序
main "$@"




