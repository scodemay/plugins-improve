#!/bin/bash

# Scheduler-Plugins 项目快速启动脚本

echo "🚀 Scheduler-Plugins 快速启动菜单"
echo "================================="
echo ""
echo "请选择要执行的操作:"
echo ""
echo "📊 监控系统:"
echo "  1. 部署监控系统"
echo "  2. 测试监控功能" 
echo "  3. 访问监控界面"
echo ""
echo "🧪 性能测试:"
echo "  4. 运行性能测试"
echo "  5. 查看测试报告"
echo ""
echo "📈 负载分析:"
echo "  6. 计算负载均衡"
echo "  7. 监控性能数据"
echo ""
echo "📚 文档查看:"
echo "  8. 查看项目结构"
echo "  9. 查看监控文档"
echo ""
echo "请输入选项 (1-9): "
read choice

case $choice in
    1) ./tools/monitoring/deploy-enhanced-monitoring.sh ;;
    2) ./tools/monitoring/test-separated-metrics.sh ;;
    3) ./tools/monitoring/access-monitoring.sh ;;
    4) ./scripts/run-performance-tests.sh ;;
    5) cat tools/testing/project-test-report.md ;;
    6) ./scripts/calculate-balance.sh ;;
    7) ./scripts/monitor-performance.sh ;;
    8) cat PROJECT_STRUCTURE.md ;;
    9) cat docs/monitoring/separated-load-balance-queries.md ;;
    *) echo "无效选项，请重新运行脚本" ;;
esac
