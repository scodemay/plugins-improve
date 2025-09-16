#!/bin/bash
# 简单的插件管理脚本 - 使用传统方式

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 配置
NAMESPACE="kube-system"
CONFIGMAP_NAME="rescheduler-config"
SCHEDULER_DEPLOYMENT="rescheduler-scheduler"

echo -e "${BLUE}=== 简单插件管理器 ===${NC}"
echo "使用传统方式管理调度器插件"
echo ""

# 检查依赖
check_dependencies() {
    if ! command -v kubectl >/dev/null 2>&1; then
        echo -e "${RED}错误: kubectl 未安装${NC}"
        exit 1
    fi
    
    if ! kubectl cluster-info >/dev/null 2>&1; then
        echo -e "${RED}错误: 无法连接到 Kubernetes 集群${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✓ 依赖检查完成${NC}"
}

# 备份当前配置
backup_config() {
    local backup_file="/tmp/scheduler-config-backup-$(date +%Y%m%d-%H%M%S).yaml"
    
    echo -e "${YELLOW}备份当前配置到: $backup_file${NC}"
    kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o yaml > "$backup_file"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 配置备份成功${NC}"
    else
        echo -e "${RED}✗ 配置备份失败${NC}"
        return 1
    fi
}

# 获取当前配置
get_current_config() {
    echo -e "${CYAN}=== 当前配置 ===${NC}"
    kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o yaml
}

# 启用插件 - 方法1：直接修改ConfigMap
enable_plugin_method1() {
    local plugin_name=$1
    local phases=${2:-"filter,score"}
    
    echo -e "${GREEN}=== 启用插件: $plugin_name (方法1) ===${NC}"
    
    # 备份配置
    backup_config
    
    # 创建新的配置
    cat > /tmp/new-config.yaml << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: $CONFIGMAP_NAME
  namespace: $NAMESPACE
data:
  config.yaml: |
    apiVersion: kubescheduler.config.k8s.io/v1
    kind: KubeSchedulerConfiguration
    profiles:
    - schedulerName: rescheduler-scheduler
      plugins:
        filter:
          enabled:
          - name: $plugin_name
        score:
          enabled:
          - name: $plugin_name
      pluginConfig:
      - name: $plugin_name
        args:
          cpuThreshold: 80.0
          memoryThreshold: 80.0
    leaderElection:
      leaderElect: true
      resourceName: rescheduler-scheduler-unique
      resourceNamespace: kube-system
EOF
    
    # 应用新配置
    kubectl apply -f /tmp/new-config.yaml
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 插件 $plugin_name 启用成功${NC}"
        restart_scheduler
    else
        echo -e "${RED}✗ 插件启用失败${NC}"
        return 1
    fi
}

# 启用插件 - 方法2：使用kubectl patch
enable_plugin_method2() {
    local plugin_name=$1
    local phases=${2:-"filter,score"}
    
    echo -e "${GREEN}=== 启用插件: $plugin_name (方法2) ===${NC}"
    
    # 备份配置
    backup_config
    
    # 使用kubectl patch更新配置
    kubectl patch configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" --type merge -p "{
      \"data\": {
        \"config.yaml\": \"apiVersion: kubescheduler.config.k8s.io/v1\\nkind: KubeSchedulerConfiguration\\nprofiles:\\n- schedulerName: rescheduler-scheduler\\n  plugins:\\n    filter:\\n      enabled:\\n      - name: $plugin_name\\n    score:\\n      enabled:\\n      - name: $plugin_name\\n  pluginConfig:\\n  - name: $plugin_name\\n    args:\\n      cpuThreshold: 80.0\\n      memoryThreshold: 80.0\\nleaderElection:\\n  leaderElect: true\\n  resourceName: rescheduler-scheduler-unique\\n  resourceNamespace: kube-system\"
      }
    }"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 插件 $plugin_name 启用成功${NC}"
        restart_scheduler
    else
        echo -e "${RED}✗ 插件启用失败${NC}"
        return 1
    fi
}

# 禁用插件
disable_plugin() {
    local plugin_name=$1
    local phases=${2:-"filter,score"}
    
    echo -e "${RED}=== 禁用插件: $plugin_name ===${NC}"
    
    # 备份配置
    backup_config
    
    # 使用kubectl patch禁用插件
    kubectl patch configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" --type merge -p "{
      \"data\": {
        \"config.yaml\": \"apiVersion: kubescheduler.config.k8s.io/v1\\nkind: KubeSchedulerConfiguration\\nprofiles:\\n- schedulerName: rescheduler-scheduler\\n  plugins:\\n    filter:\\n      disabled:\\n      - name: $plugin_name\\n    score:\\n      disabled:\\n      - name: $plugin_name\\nleaderElection:\\n  leaderElect: true\\n  resourceName: rescheduler-scheduler-unique\\n  resourceNamespace: kube-system\"
      }
    }"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 插件 $plugin_name 禁用成功${NC}"
        restart_scheduler
    else
        echo -e "${RED}✗ 插件禁用失败${NC}"
        return 1
    fi
}

# 更新插件配置
update_plugin_config() {
    local plugin_name=$1
    local config_key=$2
    local config_value=$3
    
    echo -e "${BLUE}=== 更新插件配置 ===${NC}"
    echo "插件: $plugin_name"
    echo "配置项: $config_key"
    echo "新值: $config_value"
    
    # 备份配置
    backup_config
    
    # 获取当前配置
    local current_config=$(kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o jsonpath='{.data.config\.yaml}')
    
    # 使用sed更新配置（简单示例）
    local new_config=$(echo "$current_config" | sed "s/${config_key}: [0-9.]*/${config_key}: ${config_value}/g")
    
    # 更新ConfigMap
    kubectl patch configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" --type merge -p "{
      \"data\": {
        \"config.yaml\": \"$(echo "$new_config" | sed 's/"/\\"/g' | tr '\n' '\\n')\"
      }
    }"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 插件配置更新成功${NC}"
        restart_scheduler
    else
        echo -e "${RED}✗ 插件配置更新失败${NC}"
        return 1
    fi
}

# 重启调度器
restart_scheduler() {
    echo -e "${YELLOW}重启调度器以应用配置变更...${NC}"
    
    # 检查调度器部署是否存在
    if ! kubectl get deployment "$SCHEDULER_DEPLOYMENT" -n "$NAMESPACE" >/dev/null 2>&1; then
        echo -e "${RED}错误: 调度器部署 $SCHEDULER_DEPLOYMENT 不存在${NC}"
        return 1
    fi
    
    # 重启调度器
    kubectl rollout restart deployment/"$SCHEDULER_DEPLOYMENT" -n "$NAMESPACE"
    
    # 等待重启完成
    echo "等待调度器重启完成..."
    kubectl rollout status deployment/"$SCHEDULER_DEPLOYMENT" -n "$NAMESPACE" --timeout=300s
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 调度器重启成功${NC}"
    else
        echo -e "${RED}✗ 调度器重启失败${NC}"
        return 1
    fi
}

# 显示帮助信息
show_help() {
    echo -e "${BLUE}=== 使用说明 ===${NC}"
    echo "1. 查看当前配置"
    echo "2. 启用插件 (方法1: 直接修改)"
    echo "3. 启用插件 (方法2: kubectl patch)"
    echo "4. 禁用插件"
    echo "5. 更新插件配置"
    echo "6. 重启调度器"
    echo "7. 备份配置"
    echo "8. 显示帮助"
    echo "9. 退出"
    echo ""
    echo "命令行用法:"
    echo "  $0 get-config                    # 查看当前配置"
    echo "  $0 enable-m1 <plugin> [phases]  # 启用插件(方法1)"
    echo "  $0 enable-m2 <plugin> [phases]  # 启用插件(方法2)"
    echo "  $0 disable <plugin> [phases]    # 禁用插件"
    echo "  $0 update <plugin> <key> <value> # 更新配置"
    echo "  $0 restart                      # 重启调度器"
    echo "  $0 backup                       # 备份配置"
}

# 交互式菜单
interactive_menu() {
    while true; do
        echo -e "${PURPLE}=== 简单插件管理器主菜单 ===${NC}"
        echo "1. 查看当前配置"
        echo "2. 启用插件 (方法1: 直接修改)"
        echo "3. 启用插件 (方法2: kubectl patch)"
        echo "4. 禁用插件"
        echo "5. 更新插件配置"
        echo "6. 重启调度器"
        echo "7. 备份配置"
        echo "8. 显示帮助"
        echo "9. 退出"
        echo ""
        
        read -p "请选择操作 [1-9]: " choice
        
        case $choice in
            1)
                get_current_config
                ;;
            2)
                read -p "请输入要启用的插件名称: " plugin_name
                read -p "请输入插件阶段 (默认: filter,score): " phases
                phases=${phases:-"filter,score"}
                enable_plugin_method1 "$plugin_name" "$phases"
                ;;
            3)
                read -p "请输入要启用的插件名称: " plugin_name
                read -p "请输入插件阶段 (默认: filter,score): " phases
                phases=${phases:-"filter,score"}
                enable_plugin_method2 "$plugin_name" "$phases"
                ;;
            4)
                read -p "请输入要禁用的插件名称: " plugin_name
                read -p "请输入插件阶段 (默认: filter,score): " phases
                phases=${phases:-"filter,score"}
                disable_plugin "$plugin_name" "$phases"
                ;;
            5)
                read -p "请输入插件名称: " plugin_name
                read -p "请输入配置项名称: " config_key
                read -p "请输入配置值: " config_value
                update_plugin_config "$plugin_name" "$config_key" "$config_value"
                ;;
            6)
                restart_scheduler
                ;;
            7)
                backup_config
                ;;
            8)
                show_help
                ;;
            9)
                echo -e "${GREEN}退出插件管理器${NC}"
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
    shift
    
    case $action in
        "get-config")
            get_current_config
            ;;
        "enable-m1")
            local plugin_name=$1
            local phases=${2:-"filter,score"}
            enable_plugin_method1 "$plugin_name" "$phases"
            ;;
        "enable-m2")
            local plugin_name=$1
            local phases=${2:-"filter,score"}
            enable_plugin_method2 "$plugin_name" "$phases"
            ;;
        "disable")
            local plugin_name=$1
            local phases=${2:-"filter,score"}
            disable_plugin "$plugin_name" "$phases"
            ;;
        "update")
            local plugin_name=$1
            local config_key=$2
            local config_value=$3
            update_plugin_config "$plugin_name" "$config_key" "$config_value"
            ;;
        "restart")
            restart_scheduler
            ;;
        "backup")
            backup_config
            ;;
        *)
            echo -e "${RED}无效操作: $action${NC}"
            show_help
            exit 1
            ;;
    esac
}

# 主函数
main() {
    # 检查依赖
    check_dependencies
    
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
trap 'echo -e "\n${YELLOW}脚本被中断${NC}"; exit 0' INT TERM

# 运行主程序
main "$@"
