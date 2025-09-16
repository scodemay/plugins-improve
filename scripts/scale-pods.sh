#!/bin/bash
# Kubernetes Pod扩容脚本
# 支持扩容现有的Deployment、ReplicaSet和StatefulSet

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# 配置
REPLICA_INCREMENT=10
MAX_REPLICAS=100
LOG_DIR="logs"
LOG_FILE=""

echo -e "${BLUE}=== Kubernetes Pod扩容脚本 ===${NC}"
echo "支持扩容现有的Deployment、ReplicaSet和StatefulSet"
echo "每次扩容增加 ${REPLICA_INCREMENT} 个副本"
echo ""

# 创建日志目录
mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/scale-pods-$(date +%Y%m%d-%H%M%S).log"

# 日志函数
log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S'): $1" | tee -a "$LOG_FILE"
}

# 检查依赖
check_dependencies() {
    echo -e "${YELLOW}检查依赖...${NC}"
    
    if ! command -v kubectl >/dev/null 2>&1; then
        echo -e "${RED}错误: kubectl 未安装或不在 PATH 中${NC}"
        exit 1
    fi
    
    # 检查集群连接
    if ! kubectl cluster-info >/dev/null 2>&1; then
        echo -e "${RED}错误: 无法连接到 Kubernetes 集群${NC}"
        exit 1
    fi
    
    # 检查 metrics server
    if ! kubectl top nodes >/dev/null 2>&1; then
        echo -e "${YELLOW}警告: Metrics Server 不可用，无法显示实际资源使用量${NC}"
    fi
    
    echo -e "${GREEN}✓ 依赖检查完成${NC}"
    log "依赖检查完成"
}

# 获取集群资源信息
get_cluster_resources() {
    echo -e "${CYAN}=== 集群资源概览 ===${NC}"
    
    # 获取节点信息
    echo -e "${YELLOW}## 节点信息${NC}"
    kubectl get nodes -o wide | tee -a "$LOG_FILE"
    
    # 获取节点资源使用情况
    echo -e "${YELLOW}## 节点资源使用情况${NC}"
    if kubectl top nodes >/dev/null 2>&1; then
        kubectl top nodes | tee -a "$LOG_FILE"
    else
        echo "无法获取节点资源使用情况 (Metrics Server 未启用)"
        log "无法获取节点资源使用情况"
    fi
}

# 获取当前Pod资源使用情况
get_pod_resources() {
    local namespace=${1:-""}
    local label_selector=${2:-""}
    
    echo -e "${CYAN}=== 当前Pod资源使用情况 ===${NC}"
    
    # 构建kubectl命令
    local cmd="kubectl get pods"
    if [ -n "$namespace" ]; then
        cmd="$cmd -n $namespace"
    else
        cmd="$cmd -A"
    fi
    
    if [ -n "$label_selector" ]; then
        cmd="$cmd -l $label_selector"
    fi
    
    cmd="$cmd -o wide"
    
    echo -e "${YELLOW}## Pod状态${NC}"
    eval "$cmd" | tee -a "$LOG_FILE"
    
    # 获取Pod资源使用情况
    echo -e "${YELLOW}## Pod资源使用${NC}"
    local top_cmd="kubectl top pods"
    if [ -n "$namespace" ]; then
        top_cmd="$top_cmd -n $namespace"
    else
        top_cmd="$top_cmd -A"
    fi
    
    if [ -n "$label_selector" ]; then
        top_cmd="$top_cmd -l $label_selector"
    fi
    
    if eval "$top_cmd" >/dev/null 2>&1; then
        eval "$top_cmd" | tee -a "$LOG_FILE"
    else
        echo "无法获取Pod资源使用情况"
    fi
    
    # 计算资源请求统计
    echo -e "${YELLOW}## 资源请求统计${NC}"
    local total_cpu_requests=0
    local total_memory_requests=0
    local total_cpu_limits=0
    local total_memory_limits=0
    local pod_count=0
    
    # 获取Pod列表
    local pods_cmd="kubectl get pods"
    if [ -n "$namespace" ]; then
        pods_cmd="$pods_cmd -n $namespace"
    else
        pods_cmd="$pods_cmd -A"
    fi
    
    if [ -n "$label_selector" ]; then
        pods_cmd="$pods_cmd -l $label_selector"
    fi
    
    pods_cmd="$pods_cmd --no-headers"
    
    while IFS= read -r line; do
        if [ -n "$line" ]; then
            local pod_name=$(echo "$line" | awk '{print $1}')
            local pod_namespace=$(echo "$line" | awk '{print $2}')
            
            # 获取Pod资源请求
            local cpu_req=$(kubectl get pod "$pod_name" -n "$pod_namespace" -o jsonpath='{.spec.containers[0].resources.requests.cpu}' 2>/dev/null | sed 's/m//' | sed 's/^$/0/')
            local mem_req=$(kubectl get pod "$pod_name" -n "$pod_namespace" -o jsonpath='{.spec.containers[0].resources.requests.memory}' 2>/dev/null | sed 's/Mi//' | sed 's/Gi//' | sed 's/Ki//' | sed 's/^$/0/')
            local cpu_lim=$(kubectl get pod "$pod_name" -n "$pod_namespace" -o jsonpath='{.spec.containers[0].resources.limits.cpu}' 2>/dev/null | sed 's/m//' | sed 's/^$/0/')
            local mem_lim=$(kubectl get pod "$pod_name" -n "$pod_namespace" -o jsonpath='{.spec.containers[0].resources.limits.memory}' 2>/dev/null | sed 's/Mi//' | sed 's/Gi//' | sed 's/Ki//' | sed 's/^$/0/')
            
            # 转换内存单位
            if [[ $mem_req =~ Gi ]]; then
                mem_req=$(echo "$mem_req" | sed 's/Gi//' | awk '{print $1 * 1024}')
            elif [[ $mem_req =~ Ki ]]; then
                mem_req=$(echo "$mem_req" | sed 's/Ki//' | awk '{print $1 / 1024}')
            fi
            
            if [[ $mem_lim =~ Gi ]]; then
                mem_lim=$(echo "$mem_lim" | sed 's/Gi//' | awk '{print $1 * 1024}')
            elif [[ $mem_lim =~ Ki ]]; then
                mem_lim=$(echo "$mem_lim" | sed 's/Ki//' | awk '{print $1 / 1024}')
            fi
            
            total_cpu_requests=$((total_cpu_requests + cpu_req))
            total_memory_requests=$(echo "$total_memory_requests + $mem_req" | bc -l 2>/dev/null || echo "$total_memory_requests")
            total_cpu_limits=$((total_cpu_limits + cpu_lim))
            total_memory_limits=$(echo "$total_memory_limits + $mem_lim" | bc -l 2>/dev/null || echo "$total_memory_limits")
            pod_count=$((pod_count + 1))
        fi
    done < <(eval "$pods_cmd" 2>/dev/null)
    
    echo "Pod数量: $pod_count"
    echo "总CPU请求: ${total_cpu_requests}m"
    echo "总内存请求: ${total_memory_requests}Mi"
    echo "总CPU限制: ${total_cpu_limits}m"
    echo "总内存限制: ${total_memory_limits}Mi"
    
    log "资源请求统计 - Pod数量: $pod_count, CPU: ${total_cpu_requests}m, 内存: ${total_memory_requests}Mi"
}

# 列出可扩容的资源
list_scalable_resources() {
    echo -e "${CYAN}=== 可扩容的资源 ===${NC}"
    
    # 列出所有Deployment
    echo -e "${YELLOW}## Deployments${NC}"
    local deployments=$(kubectl get deployments -A --no-headers 2>/dev/null | awk '{print $1 " " $2 " " $3 " " $4}')
    if [ -n "$deployments" ]; then
        echo "NAMESPACE    NAME                    READY   UP-TO-DATE   AVAILABLE"
        echo "$deployments" | while read -r line; do
            echo "  $line"
        done
    else
        echo "  无Deployment资源"
    fi
    
    # 列出所有ReplicaSet
    echo -e "${YELLOW}## ReplicaSets${NC}"
    local replicasets=$(kubectl get replicasets -A --no-headers 2>/dev/null | awk '{print $1 " " $2 " " $3 " " $4}')
    if [ -n "$replicasets" ]; then
        echo "NAMESPACE    NAME                    DESIRED   CURRENT   READY"
        echo "$replicasets" | while read -r line; do
            echo "  $line"
        done
    else
        echo "  无ReplicaSet资源"
    fi
    
    # 列出所有StatefulSet
    echo -e "${YELLOW}## StatefulSets${NC}"
    local statefulsets=$(kubectl get statefulsets -A --no-headers 2>/dev/null | awk '{print $1 " " $2 " " $3 " " $4}')
    if [ -n "$statefulsets" ]; then
        echo "NAMESPACE    NAME                    READY   AGE"
        echo "$statefulsets" | while read -r line; do
            echo "  $line"
        done
    else
        echo "  无StatefulSet资源"
    fi
}

# 扩容Deployment
scale_deployment() {
    local namespace=$1
    local name=$2
    local replicas=$3
    
    echo -e "${GREEN}=== 扩容Deployment: $namespace/$name ===${NC}"
    log "开始扩容Deployment: $namespace/$name 到 $replicas 个副本"
    
    # 获取当前副本数
    local current_replicas=$(kubectl get deployment "$name" -n "$namespace" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    echo "当前副本数: $current_replicas"
    echo "目标副本数: $replicas"
    
    # 执行扩容
    kubectl scale deployment "$name" -n "$namespace" --replicas="$replicas"
    
    # 等待扩容完成
    echo "等待扩容完成..."
    kubectl rollout status deployment/"$name" -n "$namespace" --timeout=300s
    
    # 显示扩容结果
    echo -e "${YELLOW}## 扩容后状态${NC}"
    kubectl get deployment "$name" -n "$namespace" -o wide
    
    # 显示Pod状态
    echo -e "${YELLOW}## Pod状态${NC}"
    kubectl get pods -n "$namespace" -l app="$name" -o wide 2>/dev/null || kubectl get pods -n "$namespace" --selector="app.kubernetes.io/name=$name" -o wide 2>/dev/null || kubectl get pods -n "$namespace" -o wide | grep "$name"
    
    log "Deployment扩容完成: $namespace/$name 到 $replicas 个副本"
}

# 扩容ReplicaSet
scale_replicaset() {
    local namespace=$1
    local name=$2
    local replicas=$3
    
    echo -e "${GREEN}=== 扩容ReplicaSet: $namespace/$name ===${NC}"
    log "开始扩容ReplicaSet: $namespace/$name 到 $replicas 个副本"
    
    # 获取当前副本数
    local current_replicas=$(kubectl get replicaset "$name" -n "$namespace" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    echo "当前副本数: $current_replicas"
    echo "目标副本数: $replicas"
    
    # 执行扩容
    kubectl scale replicaset "$name" -n "$namespace" --replicas="$replicas"
    
    # 等待扩容完成
    echo "等待扩容完成..."
    sleep 30
    
    # 显示扩容结果
    echo -e "${YELLOW}## 扩容后状态${NC}"
    kubectl get replicaset "$name" -n "$namespace" -o wide
    
    # 显示Pod状态
    echo -e "${YELLOW}## Pod状态${NC}"
    kubectl get pods -n "$namespace" -o wide | grep "$name"
    
    log "ReplicaSet扩容完成: $namespace/$name 到 $replicas 个副本"
}

# 扩容StatefulSet
scale_statefulset() {
    local namespace=$1
    local name=$2
    local replicas=$3
    
    echo -e "${GREEN}=== 扩容StatefulSet: $namespace/$name ===${NC}"
    log "开始扩容StatefulSet: $namespace/$name 到 $replicas 个副本"
    
    # 获取当前副本数
    local current_replicas=$(kubectl get statefulset "$name" -n "$namespace" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
    echo "当前副本数: $current_replicas"
    echo "目标副本数: $replicas"
    
    # 执行扩容
    kubectl scale statefulset "$name" -n "$namespace" --replicas="$replicas"
    
    # 等待扩容完成
    echo "等待扩容完成..."
    kubectl rollout status statefulset/"$name" -n "$namespace" --timeout=300s
    
    # 显示扩容结果
    echo -e "${YELLOW}## 扩容后状态${NC}"
    kubectl get statefulset "$name" -n "$namespace" -o wide
    
    # 显示Pod状态
    echo -e "${YELLOW}## Pod状态${NC}"
    kubectl get pods -n "$namespace" -l app="$name" -o wide 2>/dev/null || kubectl get pods -n "$namespace" --selector="app.kubernetes.io/name=$name" -o wide 2>/dev/null || kubectl get pods -n "$namespace" -o wide | grep "$name"
    
    log "StatefulSet扩容完成: $namespace/$name 到 $replicas 个副本"
}

# 交互式选择要扩容的资源
interactive_scale() {
    echo -e "${PURPLE}=== 选择要扩容的资源 ===${NC}"
    
    # 显示可扩容的资源
    list_scalable_resources
    
    echo ""
    echo "请选择要扩容的资源类型："
    echo "1. Deployment"
    echo "2. ReplicaSet"
    echo "3. StatefulSet"
    echo "4. 返回主菜单"
    
    read -p "请选择 [1-4]: " resource_type
    
    case $resource_type in
        1)
            scale_deployment_interactive
            ;;
        2)
            scale_replicaset_interactive
            ;;
        3)
            scale_statefulset_interactive
            ;;
        4)
            return
            ;;
        *)
            echo -e "${RED}无效选择${NC}"
            return
            ;;
    esac
}

# 交互式扩容Deployment
scale_deployment_interactive() {
    echo -e "${YELLOW}## 选择Deployment${NC}"
    
    # 获取所有Deployment
    local deployments=$(kubectl get deployments -A --no-headers 2>/dev/null)
    if [ -z "$deployments" ]; then
        echo -e "${RED}没有找到Deployment资源${NC}"
        return
    fi
    
    # 显示Deployment列表
    local index=1
    local deployment_list=()
    echo "可用的Deployment："
    while IFS= read -r line; do
        if [ -n "$line" ]; then
            local namespace=$(echo "$line" | awk '{print $1}')
            local name=$(echo "$line" | awk '{print $2}')
            local ready=$(echo "$line" | awk '{print $3}')
            local available=$(echo "$line" | awk '{print $4}')
            
            echo "$index. $namespace/$name (Ready: $ready, Available: $available)"
            deployment_list+=("$namespace $name")
            index=$((index + 1))
        fi
    done <<< "$deployments"
    
    read -p "请选择Deployment [1-$((index-1))]: " choice
    
    if [ "$choice" -ge 1 ] && [ "$choice" -lt "$index" ]; then
        local selected_deployment="${deployment_list[$((choice-1))]}"
        local namespace=$(echo "$selected_deployment" | awk '{print $1}')
        local name=$(echo "$selected_deployment" | awk '{print $2}')
        
        # 获取当前副本数
        local current_replicas=$(kubectl get deployment "$name" -n "$namespace" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
        local target_replicas=$((current_replicas + REPLICA_INCREMENT))
        
        echo "当前副本数: $current_replicas"
        echo "目标副本数: $target_replicas"
        
        read -p "确认扩容到 $target_replicas 个副本？[y/N]: " confirm
        if [[ $confirm =~ ^[Yy]$ ]]; then
            scale_deployment "$namespace" "$name" "$target_replicas"
        else
            echo "取消扩容"
        fi
    else
        echo -e "${RED}无效选择${NC}"
    fi
}

# 交互式扩容ReplicaSet
scale_replicaset_interactive() {
    echo -e "${YELLOW}## 选择ReplicaSet${NC}"
    
    # 获取所有ReplicaSet
    local replicasets=$(kubectl get replicasets -A --no-headers 2>/dev/null)
    if [ -z "$replicasets" ]; then
        echo -e "${RED}没有找到ReplicaSet资源${NC}"
        return
    fi
    
    # 显示ReplicaSet列表
    local index=1
    local replicaset_list=()
    echo "可用的ReplicaSet："
    while IFS= read -r line; do
        if [ -n "$line" ]; then
            local namespace=$(echo "$line" | awk '{print $1}')
            local name=$(echo "$line" | awk '{print $2}')
            local desired=$(echo "$line" | awk '{print $3}')
            local current=$(echo "$line" | awk '{print $4}')
            
            echo "$index. $namespace/$name (Desired: $desired, Current: $current)"
            replicaset_list+=("$namespace $name")
            index=$((index + 1))
        fi
    done <<< "$replicasets"
    
    read -p "请选择ReplicaSet [1-$((index-1))]: " choice
    
    if [ "$choice" -ge 1 ] && [ "$choice" -lt "$index" ]; then
        local selected_replicaset="${replicaset_list[$((choice-1))]}"
        local namespace=$(echo "$selected_replicaset" | awk '{print $1}')
        local name=$(echo "$selected_replicaset" | awk '{print $2}')
        
        # 获取当前副本数
        local current_replicas=$(kubectl get replicaset "$name" -n "$namespace" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
        local target_replicas=$((current_replicas + REPLICA_INCREMENT))
        
        echo "当前副本数: $current_replicas"
        echo "目标副本数: $target_replicas"
        
        read -p "确认扩容到 $target_replicas 个副本？[y/N]: " confirm
        if [[ $confirm =~ ^[Yy]$ ]]; then
            scale_replicaset "$namespace" "$name" "$target_replicas"
        else
            echo "取消扩容"
        fi
    else
        echo -e "${RED}无效选择${NC}"
    fi
}

# 交互式扩容StatefulSet
scale_statefulset_interactive() {
    echo -e "${YELLOW}## 选择StatefulSet${NC}"
    
    # 获取所有StatefulSet
    local statefulsets=$(kubectl get statefulsets -A --no-headers 2>/dev/null)
    if [ -z "$statefulsets" ]; then
        echo -e "${RED}没有找到StatefulSet资源${NC}"
        return
    fi
    
    # 显示StatefulSet列表
    local index=1
    local statefulset_list=()
    echo "可用的StatefulSet："
    while IFS= read -r line; do
        if [ -n "$line" ]; then
            local namespace=$(echo "$line" | awk '{print $1}')
            local name=$(echo "$line" | awk '{print $2}')
            local ready=$(echo "$line" | awk '{print $3}')
            local age=$(echo "$line" | awk '{print $4}')
            
            echo "$index. $namespace/$name (Ready: $ready, Age: $age)"
            statefulset_list+=("$namespace $name")
            index=$((index + 1))
        fi
    done <<< "$statefulsets"
    
    read -p "请选择StatefulSet [1-$((index-1))]: " choice
    
    if [ "$choice" -ge 1 ] && [ "$choice" -lt "$index" ]; then
        local selected_statefulset="${statefulset_list[$((choice-1))]}"
        local namespace=$(echo "$selected_statefulset" | awk '{print $1}')
        local name=$(echo "$selected_statefulset" | awk '{print $2}')
        
        # 获取当前副本数
        local current_replicas=$(kubectl get statefulset "$name" -n "$namespace" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "0")
        local target_replicas=$((current_replicas + REPLICA_INCREMENT))
        
        echo "当前副本数: $current_replicas"
        echo "目标副本数: $target_replicas"
        
        read -p "确认扩容到 $target_replicas 个副本？[y/N]: " confirm
        if [[ $confirm =~ ^[Yy]$ ]]; then
            scale_statefulset "$namespace" "$name" "$target_replicas"
        else
            echo "取消扩容"
        fi
    else
        echo -e "${RED}无效选择${NC}"
    fi
}

# 显示帮助信息
show_help() {
    echo -e "${BLUE}=== 使用说明 ===${NC}"
    echo "1. 扩容Deployment - 增加Deployment的副本数"
    echo "2. 扩容ReplicaSet - 增加ReplicaSet的副本数"
    echo "3. 扩容StatefulSet - 增加StatefulSet的副本数"
    echo "4. 查看资源状态 - 显示集群和Pod资源使用情况"
    echo "5. 列出可扩容资源 - 显示所有可扩容的资源"
    echo "6. 退出"
    echo ""
}

# 交互式菜单
interactive_menu() {
    while true; do
        echo -e "${PURPLE}=== Pod扩容脚本主菜单 ===${NC}"
        echo "1. 扩容Deployment (+10个副本)"
        echo "2. 扩容ReplicaSet (+10个副本)"
        echo "3. 扩容StatefulSet (+10个副本)"
        echo "4. 查看资源使用情况"
        echo "5. 列出可扩容资源"
        echo "6. 显示帮助"
        echo "7. 退出"
        echo ""
        
        read -p "请选择操作 [1-7]: " choice
        
        case $choice in
            1)
                scale_deployment_interactive
                ;;
            2)
                scale_replicaset_interactive
                ;;
            3)
                scale_statefulset_interactive
                ;;
            4)
                get_cluster_resources
                get_pod_resources
                ;;
            5)
                list_scalable_resources
                ;;
            6)
                show_help
                ;;
            7)
                echo -e "${GREEN}退出扩容脚本${NC}"
                log "用户退出脚本"
                exit 0
                ;;
            *)
                echo -e "${RED}无效选择，请重新输入${NC}"
                ;;
        esac
        
        echo ""
        read -p "按回车键继续..."
        echo ""
    done
}

# 非交互式模式
non_interactive_mode() {
    local action=$1
    local resource_type=$2
    local namespace=$3
    local name=$4
    local replicas=${5:-$REPLICA_INCREMENT}
    
    case $action in
        "scale")
            case $resource_type in
                "deployment")
                    if [ -z "$namespace" ] || [ -z "$name" ]; then
                        echo -e "${RED}错误: 需要指定namespace和name${NC}"
                        echo "用法: $0 scale deployment <namespace> <name> [replicas]"
                        exit 1
                    fi
                    scale_deployment "$namespace" "$name" "$replicas"
                    ;;
                "replicaset")
                    if [ -z "$namespace" ] || [ -z "$name" ]; then
                        echo -e "${RED}错误: 需要指定namespace和name${NC}"
                        echo "用法: $0 scale replicaset <namespace> <name> [replicas]"
                        exit 1
                    fi
                    scale_replicaset "$namespace" "$name" "$replicas"
                    ;;
                "statefulset")
                    if [ -z "$namespace" ] || [ -z "$name" ]; then
                        echo -e "${RED}错误: 需要指定namespace和name${NC}"
                        echo "用法: $0 scale statefulset <namespace> <name> [replicas]"
                        exit 1
                    fi
                    scale_statefulset "$namespace" "$name" "$replicas"
                    ;;
                *)
                    echo -e "${RED}错误: 不支持的资源类型: $resource_type${NC}"
                    echo "支持的资源类型: deployment, replicaset, statefulset"
                    exit 1
                    ;;
            esac
            ;;
        "status")
            get_cluster_resources
            get_pod_resources
            ;;
        "list")
            list_scalable_resources
            ;;
        *)
            echo -e "${RED}无效操作: $action${NC}"
            echo "支持的操作: scale, status, list"
            exit 1
            ;;
    esac
}

# 主函数
main() {
    # 检查依赖
    check_dependencies
    
    # 显示初始资源状态
    get_cluster_resources
    
    # 根据参数决定运行模式
    if [ $# -eq 0 ]; then
        # 交互式模式
        interactive_menu
    else
        # 非交互式模式
        non_interactive_mode "$@"
    fi
}

# 信号处理
trap 'echo -e "\n${YELLOW}脚本被中断${NC}"; log "脚本被中断"; exit 0' INT TERM

# 运行主程序
main "$@"


