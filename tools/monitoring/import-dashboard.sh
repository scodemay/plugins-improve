#!/bin/bash

# Grafana 美化面板自动导入脚本
# Author: AI Assistant
# Date: 2025-01-01

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
GRAFANA_URL="http://localhost:3000"
GRAFANA_USER="admin"
GRAFANA_PASSWORD="admin123"
DASHBOARD_FILE="monitoring/configs/beautiful-dashboard.json"

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

log_step() {
    echo -e "${PURPLE}[STEP]${NC} $1"
}

# 显示漂亮的标题
show_title() {
    echo -e "${CYAN}"
    echo "════════════════════════════════════════════════════════════════"
    echo "          🎨 Grafana 美化面板自动导入工具 🎨"
    echo "════════════════════════════════════════════════════════════════"
    echo -e "${NC}"
}

# 检查依赖
check_dependencies() {
    log_step "检查依赖环境..."
    
    # 检查curl
    if ! command -v curl &> /dev/null; then
        log_error "curl 未安装，请先安装 curl"
        exit 1
    fi
    
    # 检查jq
    if ! command -v jq &> /dev/null; then
        log_warning "jq 未安装，将使用基础JSON处理"
        USE_JQ=false
    else
        USE_JQ=true
    fi
    
    # 检查kubectl
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl 未安装，无法检查Grafana状态"
        exit 1
    fi
    
    log_success "依赖检查完成"
}

# 检查Grafana状态
check_grafana_status() {
    log_step "检查Grafana服务状态..."
    
    # 检查Grafana Pod状态
    if kubectl get pods -n monitoring -l app=grafana &> /dev/null; then
        GRAFANA_POD=$(kubectl get pods -n monitoring -l app=grafana -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
        if [ ! -z "$GRAFANA_POD" ]; then
            GRAFANA_STATUS=$(kubectl get pod -n monitoring $GRAFANA_POD -o jsonpath='{.status.phase}' 2>/dev/null)
            if [ "$GRAFANA_STATUS" = "Running" ]; then
                log_success "Grafana Pod 运行正常: $GRAFANA_POD"
            else
                log_warning "Grafana Pod 状态异常: $GRAFANA_STATUS"
            fi
        fi
    else
        log_warning "未找到Grafana Pod，请确保监控系统已部署"
    fi
}

# 启动端口转发
setup_port_forward() {
    log_step "设置端口转发..."
    
    # 检查是否已有端口转发在运行
    if pgrep -f "kubectl port-forward.*grafana.*3000" > /dev/null; then
        log_info "端口转发已存在"
    else
        log_info "启动Grafana端口转发..."
        kubectl port-forward -n monitoring svc/grafana-service 3000:3000 &
        PORT_FORWARD_PID=$!
        sleep 3
        log_success "端口转发已启动 (PID: $PORT_FORWARD_PID)"
    fi
}

# 测试Grafana连接
test_grafana_connection() {
    log_step "测试Grafana连接..."
    
    local max_attempts=10
    local attempt=1
    
    while [ $attempt -le $max_attempts ]; do
        if curl -s "$GRAFANA_URL/api/health" &> /dev/null; then
            log_success "Grafana 连接成功"
            return 0
        else
            log_info "尝试连接 Grafana... ($attempt/$max_attempts)"
            sleep 2
            ((attempt++))
        fi
    done
    
    log_error "无法连接到 Grafana，请检查服务状态"
    return 1
}

# 检查Prometheus数据源
check_prometheus_datasource() {
    log_step "检查Prometheus数据源..."
    
    # 获取数据源列表
    local response=$(curl -s -u "$GRAFANA_USER:$GRAFANA_PASSWORD" \
        "$GRAFANA_URL/api/datasources" 2>/dev/null)
    
    if echo "$response" | grep -q "prometheus"; then
        log_success "Prometheus数据源已配置"
        
        # 提取Prometheus数据源UID
        if [ "$USE_JQ" = true ]; then
            PROMETHEUS_UID=$(echo "$response" | jq -r '.[] | select(.type=="prometheus") | .uid' | head -1)
            if [ ! -z "$PROMETHEUS_UID" ] && [ "$PROMETHEUS_UID" != "null" ]; then
                log_info "Prometheus UID: $PROMETHEUS_UID"
                # 更新面板中的数据源UID
                update_datasource_uid "$PROMETHEUS_UID"
            fi
        fi
    else
        log_warning "未找到Prometheus数据源，请先配置数据源"
        create_prometheus_datasource
    fi
}

# 创建Prometheus数据源
create_prometheus_datasource() {
    log_step "创建Prometheus数据源..."
    
    local datasource_config='{
        "name": "Prometheus",
        "type": "prometheus",
        "url": "http://prometheus-service:9090",
        "access": "proxy",
        "isDefault": true,
        "basicAuth": false
    }'
    
    local response=$(curl -s -u "$GRAFANA_USER:$GRAFANA_PASSWORD" \
        -H "Content-Type: application/json" \
        -X POST \
        -d "$datasource_config" \
        "$GRAFANA_URL/api/datasources" 2>/dev/null)
    
    if echo "$response" | grep -q '"message":"Datasource added"'; then
        log_success "Prometheus数据源创建成功"
        if [ "$USE_JQ" = true ]; then
            PROMETHEUS_UID=$(echo "$response" | jq -r '.datasource.uid')
            update_datasource_uid "$PROMETHEUS_UID"
        fi
    else
        log_warning "数据源可能已存在或创建失败"
    fi
}

# 更新面板中的数据源UID
update_datasource_uid() {
    local uid="$1"
    if [ ! -z "$uid" ]; then
        log_info "更新面板中的数据源UID为: $uid"
        sed -i.bak "s/\"uid\": \"prometheus\"/\"uid\": \"$uid\"/g" "$DASHBOARD_FILE"
    fi
}

# 导入面板
import_dashboard() {
    log_step "导入美化面板..."
    
    # 检查面板文件是否存在
    if [ ! -f "$DASHBOARD_FILE" ]; then
        log_error "面板文件不存在: $DASHBOARD_FILE"
        exit 1
    fi
    
    # 准备导入数据
    local import_data=$(cat "$DASHBOARD_FILE" | jq '{dashboard: .dashboard, overwrite: true, inputs: []}' 2>/dev/null || \
                       echo '{"dashboard": '$(cat "$DASHBOARD_FILE" | jq '.dashboard')',"overwrite": true,"inputs": []}')
    
    # 导入面板
    local response=$(curl -s -u "$GRAFANA_USER:$GRAFANA_PASSWORD" \
        -H "Content-Type: application/json" \
        -X POST \
        -d "$import_data" \
        "$GRAFANA_URL/api/dashboards/db" 2>/dev/null)
    
    if echo "$response" | grep -q '"status":"success"'; then
        local dashboard_url=$(echo "$response" | jq -r '.url' 2>/dev/null || echo "/d/scheduler-plugins-beautiful/")
        log_success "面板导入成功！"
        log_info "访问地址: $GRAFANA_URL$dashboard_url"
    else
        log_error "面板导入失败"
        if [ "$USE_JQ" = true ]; then
            echo "$response" | jq .
        else
            echo "$response"
        fi
        return 1
    fi
}

# 显示访问信息
show_access_info() {
    echo ""
    log_success "🎉 美化面板导入完成！"
    echo ""
    echo -e "${CYAN}📊 访问信息:${NC}"
    echo -e "  🌐 Grafana地址: ${GREEN}$GRAFANA_URL${NC}"
    echo -e "  👤 用户名: ${GREEN}$GRAFANA_USER${NC}"
    echo -e "  🔑 密码: ${GREEN}$GRAFANA_PASSWORD${NC}"
    echo -e "  📈 面板名称: ${GREEN}🚀 Kubernetes 智能调度器监控中心${NC}"
    echo ""
    echo -e "${CYAN}🎨 面板特色:${NC}"
    echo -e "  ✨ 现代化深色主题设计"
    echo -e "  📊 实时集群状态概览"
    echo -e "  🎯 智能负载均衡分析"
    echo -e "  🏗️ 节点Pod分布可视化"
    echo -e "  🥧 负载占比饼图"
    echo -e "  📈 Worker节点性能分析"
    echo -e "  🎯 集群健康评分"
    echo -e "  🔄 服务状态监控"
    echo -e "  💡 智能优化建议"
    echo ""
    echo -e "${YELLOW}💡 使用提示:${NC}"
    echo -e "  • 面板支持节点过滤，可选择特定节点查看"
    echo -e "  • 负载均衡率 >85% 为良好状态"
    echo -e "  • 标准差越小表示负载越均衡"
    echo -e "  • 智能建议会根据当前状态给出优化建议"
    echo ""
}

# 清理函数
cleanup() {
    if [ ! -z "$PORT_FORWARD_PID" ]; then
        log_info "清理端口转发进程..."
        kill $PORT_FORWARD_PID 2>/dev/null || true
    fi
    
    # 恢复原始面板文件
    if [ -f "${DASHBOARD_FILE}.bak" ]; then
        mv "${DASHBOARD_FILE}.bak" "$DASHBOARD_FILE"
    fi
}

# 主函数
main() {
    show_title
    
    # 设置清理陷阱
    trap cleanup EXIT
    
    check_dependencies
    check_grafana_status
    setup_port_forward
    
    if test_grafana_connection; then
        check_prometheus_datasource
        if import_dashboard; then
            show_access_info
        else
            log_error "面板导入失败，请检查配置"
            exit 1
        fi
    else
        log_error "无法连接到Grafana，导入终止"
        exit 1
    fi
}

# 如果脚本被直接执行
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
