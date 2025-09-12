#!/bin/bash
# Rescheduler 性能测试自动化脚本

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 配置
NAMESPACE="perf-test"
TEST_DURATION=300  # 5分钟测试
MONITOR_INTERVAL=30

echo -e "${BLUE}=== Rescheduler 性能测试套件 ===${NC}"
echo "测试命名空间: $NAMESPACE"
echo "测试持续时间: $TEST_DURATION 秒"
echo ""

# 检查依赖
check_dependencies() {
    echo -e "${YELLOW}检查依赖...${NC}"
    
    if ! command -v kubectl >/dev/null 2>&1; then
        echo -e "${RED}错误: kubectl 未安装${NC}"
        exit 1
    fi
    
    # 检查调度器状态
    if ! kubectl get pods -n kube-system -l app=rescheduler-scheduler | grep -q Running; then
        echo -e "${RED}错误: rescheduler-scheduler 未运行${NC}"
        exit 1
    fi
    
    # 检查 metrics server
    if ! kubectl top nodes >/dev/null 2>&1; then
        echo -e "${YELLOW}警告: Metrics Server 不可用，某些测试可能受限${NC}"
    fi
    
    echo -e "${GREEN}✓ 依赖检查完成${NC}"
}

# 清理环境
cleanup_environment() {
    echo -e "${YELLOW}清理测试环境...${NC}"
    
    # 删除测试命名空间（如果存在）
    if kubectl get namespace $NAMESPACE >/dev/null 2>&1; then
        kubectl delete namespace $NAMESPACE --timeout=60s || true
    fi
    
    # 等待命名空间完全删除
    echo "等待命名空间清理完成..."
    while kubectl get namespace $NAMESPACE >/dev/null 2>&1; do
        sleep 2
    done
    
    echo -e "${GREEN}✓ 环境清理完成${NC}"
}

# 准备测试环境
prepare_environment() {
    echo -e "${YELLOW}准备测试环境...${NC}"
    
    # 创建测试命名空间
    kubectl create namespace $NAMESPACE
    
    # 等待命名空间就绪
    sleep 5
    
    echo -e "${GREEN}✓ 测试环境准备完成${NC}"
}

# 运行测试用例
run_test_case() {
    local test_name=$1
    local test_file=$2
    local wait_time=${3:-60}
    
    echo -e "${BLUE}=== 运行测试: $test_name ===${NC}"
    
    # 确保使用绝对路径
    local script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local project_root="$(dirname "$script_dir")"
    local full_test_path="$project_root/$test_file"
    
    # 应用测试用例
    if [ -f "$full_test_path" ]; then
        kubectl apply -f "$full_test_path"
        echo "测试用例已应用: $full_test_path"
    else
        echo -e "${RED}错误: 测试文件不存在 - $full_test_path${NC}"
        return 1
    fi
    
    # 等待 Pod 启动
    echo "等待 Pod 启动 ($wait_time 秒)..."
    sleep $wait_time
    
    # 检查 Pod 状态
    echo "检查 Pod 状态:"
    kubectl get pods -n $NAMESPACE -o wide
    
    # 收集初始指标
    echo "收集初始指标:"
    kubectl top nodes 2>/dev/null || echo "无法获取节点指标"
    kubectl top pods -n $NAMESPACE 2>/dev/null || echo "无法获取 Pod 指标"
}

# 测试 1: 基础调度性能
test_basic_scheduling() {
    echo -e "${BLUE}=== 测试 1: 基础调度性能 ===${NC}"
    
    run_test_case "基础调度性能" "test-cases/basic-scheduling-test.yaml" 90
    
    # 分析调度分布
    echo "### 调度分布分析:"
    kubectl get pods -n $NAMESPACE -o wide --no-headers | \
        awk '{print $7}' | sort | uniq -c | \
        awk '{printf "节点 %s: %d pods\n", $2, $1}'
    
    # 检查调度器日志
    echo "### 调度器决策日志:"
    kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=20 | \
        grep -E "(节点打分|调度预测)" | tail -5 || echo "无相关日志"
}

# 测试 2: 并发调度性能
test_concurrent_scheduling() {
    echo -e "${BLUE}=== 测试 2: 并发调度性能 ===${NC}"
    
    # 记录开始时间
    start_time=$(date +%s)
    
    run_test_case "并发调度性能" "test-cases/concurrent-scheduling-test.yaml" 120
    
    # 计算调度完成时间
    end_time=$(date +%s)
    total_time=$((end_time - start_time))
    
    echo "### 并发调度性能分析:"
    echo "总调度时间: $total_time 秒"
    
    # 统计成功调度的 Pod 数量
    running_pods=$(kubectl get pods -n $NAMESPACE --no-headers | grep -c Running || echo 0)
    pending_pods=$(kubectl get pods -n $NAMESPACE --no-headers | grep -c Pending || echo 0)
    
    echo "成功调度: $running_pods pods"
    echo "等待调度: $pending_pods pods"
    
    if [ $running_pods -gt 0 ] && [ $total_time -gt 0 ]; then
        avg_time=$(echo "scale=2; $total_time / $running_pods" | bc -l 2>/dev/null || echo "N/A")
        echo "平均调度时间: $avg_time 秒/pod"
    fi
}

# 测试 3: 负载均衡测试
test_load_balancing() {
    echo -e "${BLUE}=== 测试 3: 负载均衡测试 ===${NC}"
    
    run_test_case "负载均衡" "test-cases/imbalance-test.yaml" 120
    
    # 分析负载分布
    echo "### 负载均衡分析:"
    
    # 等待负载稳定
    echo "等待负载稳定 (60秒)..."
    sleep 60
    
    # 计算负载均衡度
    if command -v bc >/dev/null 2>&1; then
        ./scripts/calculate-balance.sh 2>/dev/null || echo "无法计算负载均衡度"
    fi
    
    # 检查重调度活动
    echo "### 重调度活动检查:"
    kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=50 | \
        grep -E "(负载不均衡|重调度)" | tail -10 || echo "无重调度活动记录"
}

# 测试 4: 资源压力测试
test_resource_pressure() {
    echo -e "${BLUE}=== 测试 4: 资源压力测试 ===${NC}"
    
    run_test_case "资源压力" "test-cases/resource-pressure-test.yaml" 120
    
    # 监控资源使用情况
    echo "### 资源使用监控:"
    for i in {1..5}; do
        echo "监控轮次 $i/5:"
        kubectl top nodes 2>/dev/null || echo "无法获取节点指标"
        kubectl top pods -n $NAMESPACE --sort-by=cpu 2>/dev/null | head -10 || echo "无法获取 Pod 指标"
        echo "---"
        sleep 30
    done
    
    # 检查调度器的资源优化决策
    echo "### 资源优化决策:"
    kubectl logs -n kube-system -l app=rescheduler-scheduler --tail=30 | \
        grep -E "(资源优化|过载|阈值)" | tail -5 || echo "无资源优化记录"
}

# 生成测试报告
generate_test_report() {
    echo -e "${GREEN}=== 生成测试报告 ===${NC}"
    
    REPORT_FILE="performance-test-report-$(date +%Y%m%d-%H%M%S).md"
    
    cat > "$REPORT_FILE" << EOF
# Rescheduler 性能测试报告

**测试时间**: $(date)
**测试环境**: Kubernetes $(kubectl version --short 2>/dev/null | grep Server | awk '{print $3}' || echo "未知版本")
**节点数量**: $(kubectl get nodes --no-headers | wc -l)

## 测试摘要

### 1. 基础调度性能
- 测试用例: basic-scheduling-test.yaml
- Pod 数量: $(kubectl get pods -n $NAMESPACE --no-headers | wc -l 2>/dev/null || echo 0)
- 调度成功率: $(kubectl get pods -n $NAMESPACE --no-headers | grep -c Running || echo 0)/$(kubectl get pods -n $NAMESPACE --no-headers | wc -l || echo 0)

### 2. 并发调度性能
- 测试用例: concurrent-scheduling-test.yaml
- 并发度: 50 pods
- 调度分布: 
$(kubectl get pods -n $NAMESPACE -o wide --no-headers 2>/dev/null | awk '{print $7}' | sort | uniq -c | awk '{printf "  - %s: %d pods\n", $2, $1}' || echo "  无数据")

### 3. 负载均衡测试
- 测试用例: imbalance-test.yaml
- 节点负载分布:
$(kubectl top nodes --no-headers 2>/dev/null | awk '{printf "  - %s: CPU %s, Memory %s\n", $1, $3, $5}' || echo "  无法获取负载数据")

### 4. 资源压力测试
- 测试用例: resource-pressure-test.yaml
- 高资源需求 Pod 调度情况:
$(kubectl get pods -n $NAMESPACE -l pressure-type=cpu --no-headers 2>/dev/null | wc -l || echo 0) 个 CPU 密集型 Pod
$(kubectl get pods -n $NAMESPACE -l pressure-type=memory --no-headers 2>/dev/null | wc -l || echo 0) 个内存密集型 Pod

## 调度器状态
- 运行状态: $(kubectl get pods -n kube-system -l app=rescheduler-scheduler --no-headers | awk '{print $3}')
- 资源使用: $(kubectl top pods -n kube-system -l app=rescheduler-scheduler --no-headers 2>/dev/null | awk '{print "CPU: "$2", Memory: "$3}' || echo "无法获取")

## 关键指标
- 总测试时间: $(($(date +%s) - test_start_time)) 秒

### 调度成功率分析
- 服务Pod成功率: $(echo "scale=2; $(kubectl get pods -n $NAMESPACE --no-headers | grep -v 'concurrent-scheduling-test' | grep -c Running || echo 0) * 100 / $(kubectl get pods -n $NAMESPACE --no-headers | grep -v 'concurrent-scheduling-test' | wc -l || echo 1)" | bc -l 2>/dev/null || echo "N/A")% (持续运行服务)
- 任务Pod成功率: $(echo "scale=2; $(kubectl get pods -n $NAMESPACE --no-headers | grep 'concurrent-scheduling-test' | grep -cE 'Running|Completed' || echo 0) * 100 / $(kubectl get pods -n $NAMESPACE --no-headers | grep 'concurrent-scheduling-test' | wc -l || echo 1)" | bc -l 2>/dev/null || echo "N/A")% (包含已完成任务)
- 总体调度成功率: $(echo "scale=2; $(kubectl get pods -n $NAMESPACE --no-headers | grep -cE 'Running|Completed' || echo 0) * 100 / $(kubectl get pods -n $NAMESPACE --no-headers | wc -l || echo 1)" | bc -l 2>/dev/null || echo "N/A")% (所有Pod)

- 节点负载标准差: $(./scripts/calculate-balance.sh 2>/dev/null | grep "标准差" | awk '{print $2}' || echo "N/A")

## 建议
1. 监控调度器资源使用，确保不超过集群资源的 5%
2. 观察负载均衡效果，标准差应小于 20%
3. 检查重调度频率，避免过于频繁的 Pod 迁移
4. 根据实际工作负载调整 CPU 和内存阈值配置

## 详细日志
查看调度器日志:
\`\`\`bash
kubectl logs -n kube-system -l app=rescheduler-scheduler
\`\`\`

查看测试 Pod 状态:
\`\`\`bash
kubectl get pods -n $NAMESPACE -o wide
\`\`\`

EOF

    echo "测试报告已生成: $REPORT_FILE"
}

# 主测试流程
main() {
    test_start_time=$(date +%s)
    
    echo -e "${BLUE}开始 Rescheduler 性能测试${NC}"
    
    # 1. 检查依赖
    check_dependencies
    
    # 2. 清理环境
    cleanup_environment
    
    # 3. 准备环境
    prepare_environment
    
    # 4. 运行测试用例
    test_basic_scheduling
    echo "等待测试稳定..."
    sleep 60
    
    test_concurrent_scheduling
    echo "等待测试稳定..."
    sleep 60
    
    test_load_balancing
    echo "等待负载均衡..."
    sleep 90
    
    test_resource_pressure
    
    # 5. 生成报告
    generate_test_report
    
    echo -e "${GREEN}=== 性能测试完成 ===${NC}"
    echo "总耗时: $(($(date +%s) - test_start_time)) 秒"
}

# 信号处理
trap 'echo -e "\n${YELLOW}测试被中断，清理环境...${NC}"; cleanup_environment; exit 0' INT TERM

# 运行测试
main "$@"
