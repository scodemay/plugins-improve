#!/bin/bash

# 部署增强的监控系统 - 支持分离的负载均衡计算
# Author: AI Assistant
# Date: 2025-09-12

set -e

# 颜色定义
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

# 检查依赖
check_dependencies() {
    log_info "检查依赖..."
    
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl 未安装或不在PATH中"
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        log_error "无法连接到Kubernetes集群"
        exit 1
    fi
    
    log_success "依赖检查通过"
}

# 创建命名空间
create_namespace() {
    log_info "创建monitoring命名空间..."
    kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -
    log_success "命名空间已就绪"
}

# 部署Prometheus
deploy_prometheus() {
    log_info "部署Prometheus..."
    
    if [ ! -f "monitoring/deployments/prometheus-deployment.yaml" ]; then
        log_error "未找到Prometheus部署配置文件"
        exit 1
    fi
    
    kubectl apply -f monitoring/deployments/prometheus-deployment.yaml
    
    # 等待Pod启动
    log_info "等待Prometheus启动..."
    kubectl wait --for=condition=ready pod -l app=prometheus -n monitoring --timeout=120s
    
    log_success "Prometheus部署完成"
}

# 部署Grafana
deploy_grafana() {
    log_info "部署Grafana..."
    
    if [ ! -f "monitoring/deployments/grafana-deployment.yaml" ]; then
        log_error "未找到Grafana部署配置文件"
        exit 1
    fi
    
    kubectl apply -f monitoring/deployments/grafana-deployment.yaml
    
    # 等待Pod启动
    log_info "等待Grafana启动..."
    kubectl wait --for=condition=ready pod -l app=grafana -n monitoring --timeout=120s
    
    log_success "Grafana部署完成"
}

# 配置Grafana数据源和导入面板
configure_grafana() {
    log_info "配置Grafana数据源和面板..."
    
    # 等待Grafana完全启动
    sleep 10
    
    # 启动端口转发
    kubectl port-forward -n monitoring svc/grafana-service 3000:3000 > /tmp/grafana-port-forward.log 2>&1 &
    GRAFANA_PID=$!
    sleep 5
    
    # 验证数据源是否自动配置
    log_info "验证数据源配置..."
    if curl -s -u "admin:admin123" "http://localhost:3000/api/datasources" | grep -q "prometheus-datasource"; then
        log_success "Prometheus数据源已自动配置"
    else
        log_warning "数据源未自动配置，手动添加..."
        curl -s -u "admin:admin123" -X POST \
          -H "Content-Type: application/json" \
          -d '{
            "name": "Prometheus",
            "type": "prometheus",
            "access": "proxy",
            "url": "http://prometheus-service:9090",
            "uid": "prometheus-datasource",
            "basicAuth": false,
            "isDefault": true
          }' \
          http://localhost:3000/api/datasources > /dev/null
        log_success "手动配置数据源完成"
    fi
    
    # 导入面板
    if [ -f "monitoring/configs/enhanced-dashboard.json" ]; then
        log_info "导入监控面板..."
        
        # 更新面板中的数据源UID
        sed -i 's/"uid": "[^"]*"/"uid": "prometheus-datasource"/g' monitoring/configs/enhanced-dashboard.json
        
        IMPORT_RESULT=$(curl -s -u "admin:admin123" \
          -H "Content-Type: application/json" \
          -X POST \
          -d @monitoring/configs/enhanced-dashboard.json \
          http://localhost:3000/api/dashboards/import)
        
        if echo "$IMPORT_RESULT" | grep -q "imported"; then
            log_success "监控面板导入成功"
        else
            log_warning "面板导入可能有问题，请手动检查"
        fi
    else
        log_warning "未找到监控面板配置文件"
    fi
    
    # 停止端口转发
    kill $GRAFANA_PID 2>/dev/null || true
}

# 部署增强的指标收集器
deploy_enhanced_collector() {
    log_info "部署增强的指标收集器..."
    
    if [ ! -f "monitoring/deployments/enhanced-metrics-collector.yaml" ]; then
        log_error "未找到增强指标收集器配置文件"
        exit 1
    fi
    
    kubectl apply -f monitoring/deployments/enhanced-metrics-collector.yaml
    
    # 等待Pod启动
    log_info "等待增强指标收集器启动..."
    kubectl wait --for=condition=ready pod -l app=enhanced-metrics-collector -n monitoring --timeout=120s
    
    log_success "增强指标收集器部署完成"
}


# 验证部署
verify_deployment() {
    log_info "验证部署状态..."
    
    echo ""
    log_info "检查Pod状态:"
    kubectl get pods -n monitoring
    
    echo ""
    log_info "检查服务状态:"
    kubectl get svc -n monitoring
    
    echo ""
    log_info "检查组件状态:"
    echo "Prometheus: $(kubectl get pods -n monitoring -l app=prometheus --no-headers | awk '{print $3}')"
    echo "Grafana: $(kubectl get pods -n monitoring -l app=grafana --no-headers | awk '{print $3}')"
    echo "增强指标收集器: $(kubectl get pods -n monitoring -l app=enhanced-metrics-collector --no-headers | awk '{print $3}')"
    
    echo ""
    log_info "检查增强指标收集器日志:"
    kubectl logs -l app=enhanced-metrics-collector -n monitoring --tail=5
    
    echo ""
    log_info "测试增强指标端点:"
    if kubectl port-forward -n monitoring svc/enhanced-rescheduler-metrics-service 8081:8080 --timeout=5s > /dev/null 2>&1 &
    then
        FORWARD_PID=$!
        sleep 3
        if curl -s http://localhost:8081/metrics | grep -q "node_type"; then
            log_success "增强指标端点正常工作"
        else
            log_warning "增强指标端点可能有问题"
        fi
        kill $FORWARD_PID 2>/dev/null || true
    fi
}

# 生成访问信息
generate_access_info() {
    log_info "生成访问信息..."
    
    cat << EOF

🎉 完整监控系统部署成功！

📊 访问地址:
  - Grafana: http://localhost:3000 (admin/admin123)
  - Prometheus: http://localhost:9090
  - 增强指标: http://localhost:8081/metrics

🔧 端口转发命令:
  kubectl port-forward -n monitoring svc/grafana-service 3000:3000 &
  kubectl port-forward -n monitoring svc/prometheus-service 9090:9090 &
  kubectl port-forward -n monitoring svc/enhanced-rescheduler-metrics-service 8081:8080 &

📈 已部署组件:
  ✅ Prometheus - 时序数据库
  ✅ Grafana - 可视化面板
  ✅ 增强指标收集器 - Pod分布统计

📈 新功能:
  ✅ Worker节点和Control-plane分离计算
  ✅ 更准确的负载均衡率
  ✅ 节点类型标签支持
  ✅ 增强的汇总指标

📚 使用文档:
  - separated-load-balance-queries.md - 分离式查询指南
  - enhanced-metrics-collector.yaml - 增强收集器配置
  - enhanced-prometheus-config.yaml - 增强Prometheus配置

🎯 推荐查询:
  # Worker节点负载均衡率
  (1 - (stddev(rescheduler_node_pods_count{node_type="worker"}) / avg(rescheduler_node_pods_count{node_type="worker"}))) * 100
  
  # Worker节点Pod分布
  rescheduler_node_pods_count{node_type="worker"}

EOF
}

# 主函数
main() {
    echo ""
    log_info "🚀 开始部署增强的监控系统..."
    echo ""
    
    check_dependencies
    create_namespace
    deploy_prometheus
    deploy_grafana
    deploy_enhanced_collector
    configure_grafana
    verify_deployment
    generate_access_info
    
    log_success "🎉 完整监控系统部署完成！"
}

# 错误处理
trap 'log_error "部署过程中发生错误，请检查上述输出"; exit 1' ERR

# 运行主函数
main "$@"
