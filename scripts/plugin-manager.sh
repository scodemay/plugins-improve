#!/bin/bash
# 插件实时管理脚本
# 支持插件的启用、禁用、配置更新等操作

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m'

# 配置
NAMESPACE="kube-system"
CONFIGMAP_NAME="rescheduler-config"
SCHEDULER_DEPLOYMENT="rescheduler-scheduler"
LOG_DIR="logs"
LOG_FILE=""

# 支持的插件列表
SUPPORTED_PLUGINS=(
    "Rescheduler"
    "Coscheduling"
    "CapacityScheduling"
    "NodeResourceTopologyMatch"
    "NodeResourcesAllocatable"
    "TargetLoadPacking"
    "LoadVariationRiskBalancing"
    "PreemptionToleration"
    "PodState"
    "QoS"
    "SySched"
    "Trimaran"
)

# 插件阶段映射
PLUGIN_PHASES=(
    "filter"
    "score"
    "reserve"
    "preBind"
    "preFilter"
    "postFilter"
    "permit"
    "bind"
    "postBind"
)

echo -e "${BLUE}=== Kubernetes 调度器插件管理器 ===${NC}"
echo "支持插件的实时启用、禁用和配置管理"
echo ""

# 创建日志目录
mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/plugin-manager-$(date +%Y%m%d-%H%M%S).log"

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
    
    if ! kubectl cluster-info >/dev/null 2>&1; then
        echo -e "${RED}错误: 无法连接到 Kubernetes 集群${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✓ 依赖检查完成${NC}"
    log "依赖检查完成"
}

# 获取当前插件配置
get_current_config() {
    echo -e "${CYAN}=== 当前插件配置 ===${NC}"
    
    if kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
        echo -e "${YELLOW}## 当前ConfigMap配置${NC}"
        kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o yaml | tee -a "$LOG_FILE"
        
        echo -e "${YELLOW}## 当前启用的插件${NC}"
        kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o jsonpath='{.data.config\.yaml}' | \
            grep -A 20 "plugins:" | grep -E "(enabled|disabled)" | tee -a "$LOG_FILE"
    else
        echo -e "${RED}错误: ConfigMap $CONFIGMAP_NAME 不存在${NC}"
        return 1
    fi
}

# 备份当前配置
backup_config() {
    local backup_file="$LOG_DIR/config-backup-$(date +%Y%m%d-%H%M%S).yaml"
    
    echo -e "${YELLOW}备份当前配置到: $backup_file${NC}"
    
    kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o yaml > "$backup_file"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 配置备份成功${NC}"
        log "配置备份成功: $backup_file"
    else
        echo -e "${RED}✗ 配置备份失败${NC}"
        return 1
    fi
}

# 启用插件
enable_plugin() {
    local plugin_name=$1
    local phases=${2:-"filter,score"}
    
    echo -e "${GREEN}=== 启用插件: $plugin_name ===${NC}"
    log "开始启用插件: $plugin_name, 阶段: $phases"
    
    # 验证插件名称
    if ! printf '%s\n' "${SUPPORTED_PLUGINS[@]}" | grep -q "^$plugin_name$"; then
        echo -e "${RED}错误: 不支持的插件名称: $plugin_name${NC}"
        echo "支持的插件: ${SUPPORTED_PLUGINS[*]}"
        return 1
    fi
    
    # 备份当前配置
    backup_config
    
    # 获取当前配置
    local current_config=$(kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o jsonpath='{.data.config\.yaml}')
    
    # 解析阶段列表
    IFS=',' read -ra PHASE_ARRAY <<< "$phases"
    
    # 为每个阶段添加插件到enabled列表
    for phase in "${PHASE_ARRAY[@]}"; do
        if ! printf '%s\n' "${PLUGIN_PHASES[@]}" | grep -q "^$phase$"; then
            echo -e "${RED}错误: 不支持的插件阶段: $phase${NC}"
            echo "支持的阶段: ${PLUGIN_PHASES[*]}"
            return 1
        fi
        
        # 检查插件是否已经在enabled列表中
        if echo "$current_config" | grep -A 10 "plugins:" | grep -A 5 "$phase:" | grep -q "enabled:"; then
            if echo "$current_config" | grep -A 10 "plugins:" | grep -A 5 "$phase:" | grep -A 10 "enabled:" | grep -q "$plugin_name"; then
                echo -e "${YELLOW}插件 $plugin_name 在阶段 $phase 中已经启用${NC}"
                continue
            fi
        fi
        
        # 添加插件到enabled列表
        echo -e "${YELLOW}在阶段 $phase 中启用插件 $plugin_name${NC}"
        
        # 这里需要更复杂的YAML处理，暂时使用sed进行简单处理
        # 实际生产环境中应该使用yq或类似的YAML处理工具
        local temp_config=$(mktemp)
        echo "$current_config" > "$temp_config"
        
        # 使用Python脚本进行YAML处理
        python3 -c "
import yaml
import sys

# 读取当前配置
with open('$temp_config', 'r') as f:
    config = yaml.safe_load(f)

# 确保plugins结构存在
if 'profiles' not in config:
    config['profiles'] = [{'schedulerName': 'rescheduler-scheduler', 'plugins': {}}]

profile = config['profiles'][0]
if 'plugins' not in profile:
    profile['plugins'] = {}

if '$phase' not in profile['plugins']:
    profile['plugins']['$phase'] = {'enabled': [], 'disabled': []}

if 'enabled' not in profile['plugins']['$phase']:
    profile['plugins']['$phase']['enabled'] = []

# 添加插件到enabled列表
if '$plugin_name' not in profile['plugins']['$phase']['enabled']:
    profile['plugins']['$phase']['enabled'].append('$plugin_name')

# 从disabled列表中移除（如果存在）
if 'disabled' in profile['plugins']['$phase'] and '$plugin_name' in profile['plugins']['$phase']['disabled']:
    profile['plugins']['$phase']['disabled'].remove('$plugin_name')

# 写回配置
with open('$temp_config', 'w') as f:
    yaml.dump(config, f, default_flow_style=False)
" 2>/dev/null || {
            echo -e "${RED}错误: YAML处理失败，请确保安装了python3和PyYAML${NC}"
            rm -f "$temp_config"
            return 1
        }
        
        current_config=$(cat "$temp_config")
        rm -f "$temp_config"
    done
    
    # 更新ConfigMap
    echo -e "${YELLOW}更新ConfigMap...${NC}"
    kubectl patch configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" --type merge -p "{\"data\":{\"config.yaml\":\"$(echo "$current_config" | sed 's/"/\\"/g' | tr '\n' '\\n')\"}}"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 插件 $plugin_name 启用成功${NC}"
        log "插件启用成功: $plugin_name"
        
        # 重启调度器以应用配置
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
    log "开始禁用插件: $plugin_name, 阶段: $phases"
    
    # 验证插件名称
    if ! printf '%s\n' "${SUPPORTED_PLUGINS[@]}" | grep -q "^$plugin_name$"; then
        echo -e "${RED}错误: 不支持的插件名称: $plugin_name${NC}"
        echo "支持的插件: ${SUPPORTED_PLUGINS[*]}"
        return 1
    fi
    
    # 备份当前配置
    backup_config
    
    # 获取当前配置
    local current_config=$(kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o jsonpath='{.data.config\.yaml}')
    
    # 解析阶段列表
    IFS=',' read -ra PHASE_ARRAY <<< "$phases"
    
    # 为每个阶段从enabled列表移除插件，添加到disabled列表
    for phase in "${PHASE_ARRAY[@]}"; do
        if ! printf '%s\n' "${PLUGIN_PHASES[@]}" | grep -q "^$phase$"; then
            echo -e "${RED}错误: 不支持的插件阶段: $phase${NC}"
            echo "支持的阶段: ${PLUGIN_PHASES[*]}"
            return 1
        fi
        
        echo -e "${YELLOW}在阶段 $phase 中禁用插件 $plugin_name${NC}"
        
        # 使用Python脚本进行YAML处理
        local temp_config=$(mktemp)
        echo "$current_config" > "$temp_config"
        
        python3 -c "
import yaml
import sys

# 读取当前配置
with open('$temp_config', 'r') as f:
    config = yaml.safe_load(f)

# 确保plugins结构存在
if 'profiles' not in config:
    config['profiles'] = [{'schedulerName': 'rescheduler-scheduler', 'plugins': {}}]

profile = config['profiles'][0]
if 'plugins' not in profile:
    profile['plugins'] = {}

if '$phase' not in profile['plugins']:
    profile['plugins']['$phase'] = {'enabled': [], 'disabled': []}

if 'enabled' not in profile['plugins']['$phase']:
    profile['plugins']['$phase']['enabled'] = []
if 'disabled' not in profile['plugins']['$phase']:
    profile['plugins']['$phase']['disabled'] = []

# 从enabled列表中移除插件
if '$plugin_name' in profile['plugins']['$phase']['enabled']:
    profile['plugins']['$phase']['enabled'].remove('$plugin_name')

# 添加到disabled列表
if '$plugin_name' not in profile['plugins']['$phase']['disabled']:
    profile['plugins']['$phase']['disabled'].append('$plugin_name')

# 写回配置
with open('$temp_config', 'w') as f:
    yaml.dump(config, f, default_flow_style=False)
" 2>/dev/null || {
            echo -e "${RED}错误: YAML处理失败，请确保安装了python3和PyYAML${NC}"
            rm -f "$temp_config"
            return 1
        }
        
        current_config=$(cat "$temp_config")
        rm -f "$temp_config"
    done
    
    # 更新ConfigMap
    echo -e "${YELLOW}更新ConfigMap...${NC}"
    kubectl patch configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" --type merge -p "{\"data\":{\"config.yaml\":\"$(echo "$current_config" | sed 's/"/\\"/g' | tr '\n' '\\n')\"}}"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 插件 $plugin_name 禁用成功${NC}"
        log "插件禁用成功: $plugin_name"
        
        # 重启调度器以应用配置
        restart_scheduler
    else
        echo -e "${RED}✗ 插件禁用失败${NC}"
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
        log "调度器重启成功"
    else
        echo -e "${RED}✗ 调度器重启失败${NC}"
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
    
    log "开始更新插件配置: $plugin_name.$config_key = $config_value"
    
    # 备份当前配置
    backup_config
    
    # 获取当前配置
    local current_config=$(kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o jsonpath='{.data.config\.yaml}')
    
    # 使用Python脚本更新配置
    local temp_config=$(mktemp)
    echo "$current_config" > "$temp_config"
    
    python3 -c "
import yaml
import sys

# 读取当前配置
with open('$temp_config', 'r') as f:
    config = yaml.safe_load(f)

# 确保pluginConfig结构存在
if 'profiles' not in config:
    config['profiles'] = [{'schedulerName': 'rescheduler-scheduler', 'pluginConfig': []}]

profile = config['profiles'][0]
if 'pluginConfig' not in profile:
    profile['pluginConfig'] = []

# 查找插件配置
plugin_config = None
for pc in profile['pluginConfig']:
    if pc.get('name') == '$plugin_name':
        plugin_config = pc
        break

if plugin_config is None:
    # 创建新的插件配置
    plugin_config = {'name': '$plugin_name', 'args': {}}
    profile['pluginConfig'].append(plugin_config)

# 更新配置值
if 'args' not in plugin_config:
    plugin_config['args'] = {}

# 处理不同类型的值
value = '$config_value'
if value.lower() in ['true', 'false']:
    plugin_config['args']['$config_key'] = value.lower() == 'true'
elif value.isdigit():
    plugin_config['args']['$config_key'] = int(value)
elif value.replace('.', '').isdigit():
    plugin_config['args']['$config_key'] = float(value)
else:
    plugin_config['args']['$config_key'] = value

# 写回配置
with open('$temp_config', 'w') as f:
    yaml.dump(config, f, default_flow_style=False)
" 2>/dev/null || {
        echo -e "${RED}错误: YAML处理失败，请确保安装了python3和PyYAML${NC}"
        rm -f "$temp_config"
        return 1
    }
    
    current_config=$(cat "$temp_config")
    rm -f "$temp_config"
    
    # 更新ConfigMap
    echo -e "${YELLOW}更新ConfigMap...${NC}"
    kubectl patch configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" --type merge -p "{\"data\":{\"config.yaml\":\"$(echo "$current_config" | sed 's/"/\\"/g' | tr '\n' '\\n')\"}}"
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✓ 插件配置更新成功${NC}"
        log "插件配置更新成功: $plugin_name.$config_key = $config_value"
        
        # 重启调度器以应用配置
        restart_scheduler
    else
        echo -e "${RED}✗ 插件配置更新失败${NC}"
        return 1
    fi
}

# 列出所有插件状态
list_plugins() {
    echo -e "${CYAN}=== 插件状态列表 ===${NC}"
    
    if ! kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" >/dev/null 2>&1; then
        echo -e "${RED}错误: ConfigMap $CONFIGMAP_NAME 不存在${NC}"
        return 1
    fi
    
    local config=$(kubectl get configmap "$CONFIGMAP_NAME" -n "$NAMESPACE" -o jsonpath='{.data.config\.yaml}')
    
    echo -e "${YELLOW}## 启用的插件${NC}"
    for phase in "${PLUGIN_PHASES[@]}"; do
        local enabled_plugins=$(echo "$config" | grep -A 10 "plugins:" | grep -A 5 "$phase:" | grep -A 10 "enabled:" | grep "- name:" | sed 's/.*- name: //' | tr '\n' ' ')
        if [ -n "$enabled_plugins" ]; then
            echo "  $phase: $enabled_plugins"
        fi
    done
    
    echo -e "${YELLOW}## 禁用的插件${NC}"
    for phase in "${PLUGIN_PHASES[@]}"; do
        local disabled_plugins=$(echo "$config" | grep -A 10 "plugins:" | grep -A 5 "$phase:" | grep -A 10 "disabled:" | grep "- name:" | sed 's/.*- name: //' | tr '\n' ' ')
        if [ -n "$disabled_plugins" ]; then
            echo "  $phase: $disabled_plugins"
        fi
    done
    
    echo -e "${YELLOW}## 插件配置${NC}"
    echo "$config" | grep -A 20 "pluginConfig:" | grep -E "(name:|args:)" | head -20
}

# 显示帮助信息
show_help() {
    echo -e "${BLUE}=== 使用说明 ===${NC}"
    echo "1. 启用插件 - 在指定阶段启用插件"
    echo "2. 禁用插件 - 在指定阶段禁用插件"
    echo "3. 更新配置 - 更新插件的配置参数"
    echo "4. 列出状态 - 显示所有插件的当前状态"
    echo "5. 重启调度器 - 重启调度器以应用配置"
    echo "6. 显示帮助 - 显示此帮助信息"
    echo "7. 退出"
    echo ""
    echo "支持的插件: ${SUPPORTED_PLUGINS[*]}"
    echo "支持的阶段: ${PLUGIN_PHASES[*]}"
    echo ""
}

# 交互式菜单
interactive_menu() {
    while true; do
        echo -e "${PURPLE}=== 插件管理器主菜单 ===${NC}"
        echo "1. 启用插件"
        echo "2. 禁用插件"
        echo "3. 更新插件配置"
        echo "4. 列出插件状态"
        echo "5. 重启调度器"
        echo "6. 显示帮助"
        echo "7. 退出"
        echo ""
        
        read -p "请选择操作 [1-7]: " choice
        
        case $choice in
            1)
                read -p "请输入要启用的插件名称: " plugin_name
                read -p "请输入插件阶段 (默认: filter,score): " phases
                phases=${phases:-"filter,score"}
                enable_plugin "$plugin_name" "$phases"
                ;;
            2)
                read -p "请输入要禁用的插件名称: " plugin_name
                read -p "请输入插件阶段 (默认: filter,score): " phases
                phases=${phases:-"filter,score"}
                disable_plugin "$plugin_name" "$phases"
                ;;
            3)
                read -p "请输入插件名称: " plugin_name
                read -p "请输入配置项名称: " config_key
                read -p "请输入配置值: " config_value
                update_plugin_config "$plugin_name" "$config_key" "$config_value"
                ;;
            4)
                list_plugins
                ;;
            5)
                restart_scheduler
                ;;
            6)
                show_help
                ;;
            7)
                echo -e "${GREEN}退出插件管理器${NC}"
                log "用户退出插件管理器"
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
        "enable")
            local plugin_name=$1
            local phases=${2:-"filter,score"}
            enable_plugin "$plugin_name" "$phases"
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
        "list")
            list_plugins
            ;;
        "restart")
            restart_scheduler
            ;;
        *)
            echo -e "${RED}无效操作: $action${NC}"
            echo "支持的操作: enable, disable, update, list, restart"
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
trap 'echo -e "\n${YELLOW}脚本被中断${NC}"; log "脚本被中断"; exit 0' INT TERM

# 运行主程序
main "$@"
