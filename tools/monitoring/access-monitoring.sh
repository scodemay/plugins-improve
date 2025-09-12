#!/bin/bash

echo "🌐 Rescheduler监控系统访问指南"
echo "=================================="
echo ""

# 检查服务状态
echo "📊 检查监控服务状态..."
kubectl get pods -n monitoring

echo ""
echo "🔗 访问地址:"
echo "------------"
echo "Grafana (推荐): http://localhost:3000  (端口转发)"
echo "Prometheus:     http://localhost:30090 (NodePort)"
echo ""

echo "🔐 登录信息:"
echo "------------"
echo "Grafana用户名: admin"
echo "Grafana密码:   admin123"
echo ""

echo "📈 仪表板功能:"
echo "--------------"
echo "✅ 各节点Pod数量实时分布"
echo "✅ Pod分布趋势图"
echo "✅ 负载均衡度分析"
echo "✅ 集群Pod总数监控"
echo "✅ 节点Pod分布表格"
echo ""

echo "🎯 仪表板直接链接:"
echo "http://localhost:3000/d/d5b6ba70-b041-47b0-98ad-09ebc0dc1732/rescheduler-pod"
echo ""

# 检查端口转发状态
if pgrep -f "kubectl port-forward.*grafana-service" > /dev/null; then
    echo "✅ Grafana端口转发正在运行"
else
    echo "⚠️  Grafana端口转发未运行，启动中..."
    kubectl port-forward -n monitoring svc/grafana-service 3000:3000 > /dev/null 2>&1 &
    echo "✅ Grafana端口转发已启动"
fi

echo ""
echo "🧪 测试指标收集:"
echo "----------------"

# 建立指标端口转发
if ! pgrep -f "kubectl port-forward.*rescheduler-metrics-service" > /dev/null; then
    kubectl port-forward -n monitoring svc/rescheduler-metrics-service 8080:8080 > /dev/null 2>&1 &
    sleep 3
fi

echo "📊 当前Pod分布:"
kubectl get pods -n perf-test -o wide --no-headers | awk '{print $7}' | sort | uniq -c | while read count node; do
    echo "  节点 $node: $count 个Pod"
done

echo ""
echo "📡 指标端点测试:"
curl -s http://localhost:8080/metrics | head -10 || echo "指标端点暂不可用，请等待几分钟..."

echo ""
echo "💡 使用提示:"
echo "------------"
echo "1. 打开浏览器访问 http://localhost:3000"
echo "2. 使用 admin/admin123 登录"
echo "3. 查看 'Rescheduler Pod分布监控' 仪表板"
echo "4. 观察Pod在各节点间的分布变化"
echo "5. 监控负载均衡度的实时变化"
echo ""

echo "🔧 故障排除:"
echo "-------------"
echo "如果看不到数据，请检查:"
echo "- kubectl get pods -n monitoring  # 所有Pod应为Running状态"
echo "- kubectl logs -n monitoring -l app=metrics-collector  # 查看指标收集器日志"
echo "- curl http://localhost:8080/metrics  # 测试指标端点"
echo ""

# 显示当前Pod总数
TOTAL_PODS=$(kubectl get pods --all-namespaces --no-headers | grep -v "kube-system\|monitoring" | wc -l)
echo "📊 当前监控中的Pod总数: $TOTAL_PODS"

# 清理临时文件
rm -f /tmp/rescheduler-dashboard.json 2>/dev/null || true
