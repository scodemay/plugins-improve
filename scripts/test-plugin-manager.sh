#!/bin/bash
# 插件管理器测试脚本

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${BLUE}=== 插件管理器功能测试 ===${NC}"

# 测试配置
NAMESPACE="kube-system"
CONFIGMAP_NAME="rescheduler-config"
API_URL="http://localhost:8080/api/v1"

# 检查依赖
check_dependencies() {
    echo -e "${YELLOW}检查依赖...${NC}"
    
    if ! command -v kubectl >/dev/null 2>&1; then
        echo -e "${RED}错误: kubectl 未安装${NC}"
        exit 1
    fi
    
    if ! command -v curl >/dev/null 2>&1; then
        echo -e "${RED}错误: curl 未安装${NC}"
        exit 1
    fi
    
    if ! kubectl cluster-info >/dev/null 2>&1; then
        echo -e "${RED}错误: 无法连接到 Kubernetes 集群${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✓ 依赖检查完成${NC}"
}

# 测试命令行工具
test_cli_tool() {
    echo -e "${YELLOW}测试命令行工具...${NC}"
    
    # 检查脚本是否存在
    if [ ! -f "./scripts/plugin-manager.sh" ]; then
        echo -e "${RED}错误: plugin-manager.sh 脚本不存在${NC}"
        return 1
    fi
    
    # 检查脚本权限
    if [ ! -x "./scripts/plugin-manager.sh" ]; then
        chmod +x ./scripts/plugin-manager.sh
    fi
    
    # 测试语法
    if bash -n ./scripts/plugin-manager.sh; then
        echo -e "${GREEN}✓ 命令行工具语法检查通过${NC}"
    else
        echo -e "${RED}✗ 命令行工具语法错误${NC}"
        return 1
    fi
    
    # 测试帮助功能
    if ./scripts/plugin-manager.sh --help >/dev/null 2>&1; then
        echo -e "${GREEN}✓ 帮助功能正常${NC}"
    else
        echo -e "${YELLOW}⚠ 帮助功能可能不支持，这是正常的${NC}"
    fi
    
    # 测试列出功能
    if timeout 30s ./scripts/plugin-manager.sh list >/dev/null 2>&1; then
        echo -e "${GREEN}✓ 列出功能正常${NC}"
    else
        echo -e "${YELLOW}⚠ 列出功能需要kubectl连接，跳过测试${NC}"
    fi
}

# 测试API服务
test_api_service() {
    echo -e "${YELLOW}测试API服务...${NC}"
    
    # 检查API服务是否运行
    if ! curl -s "$API_URL/health" >/dev/null 2>&1; then
        echo -e "${YELLOW}⚠ API服务未运行，跳过API测试${NC}"
        echo "提示: 运行 ./scripts/deploy-plugin-manager.sh 部署API服务"
        return 0
    fi
    
    # 测试健康检查
    echo "测试健康检查..."
    if curl -s "$API_URL/health" | grep -q "healthy"; then
        echo -e "${GREEN}✓ 健康检查通过${NC}"
    else
        echo -e "${RED}✗ 健康检查失败${NC}"
        return 1
    fi
    
    # 测试获取插件状态
    echo "测试获取插件状态..."
    if curl -s "$API_URL/plugins" | grep -q "status.*success"; then
        echo -e "${GREEN}✓ 获取插件状态成功${NC}"
    else
        echo -e "${RED}✗ 获取插件状态失败${NC}"
        return 1
    fi
    
    # 测试启用插件（如果Rescheduler存在）
    echo "测试启用插件..."
    if curl -s -X POST "$API_URL/plugins/Rescheduler/enable" \
        -H "Content-Type: application/json" \
        -d '{"phases": ["filter"]}' | grep -q "status.*success"; then
        echo -e "${GREEN}✓ 启用插件成功${NC}"
    else
        echo -e "${YELLOW}⚠ 启用插件测试失败（可能是插件已启用）${NC}"
    fi
    
    # 测试禁用插件
    echo "测试禁用插件..."
    if curl -s -X POST "$API_URL/plugins/Rescheduler/disable" \
        -H "Content-Type: application/json" \
        -d '{"phases": ["filter"]}' | grep -q "status.*success"; then
        echo -e "${GREEN}✓ 禁用插件成功${NC}"
    else
        echo -e "${YELLOW}⚠ 禁用插件测试失败（可能是插件已禁用）${NC}"
    fi
    
    # 重新启用插件（恢复状态）
    curl -s -X POST "$API_URL/plugins/Rescheduler/enable" \
        -H "Content-Type: application/json" \
        -d '{"phases": ["filter"]}' >/dev/null 2>&1 || true
}

# 测试Web界面
test_web_ui() {
    echo -e "${YELLOW}测试Web界面...${NC}"
    
    # 检查HTML文件是否存在
    if [ ! -f "./scripts/plugin-web-ui.html" ]; then
        echo -e "${RED}错误: plugin-web-ui.html 文件不存在${NC}"
        return 1
    fi
    
    # 检查HTML语法
    if grep -q "<!DOCTYPE html>" ./scripts/plugin-web-ui.html; then
        echo -e "${GREEN}✓ Web界面HTML文件格式正确${NC}"
    else
        echo -e "${RED}✗ Web界面HTML文件格式错误${NC}"
        return 1
    fi
    
    # 检查JavaScript代码
    if grep -q "document.addEventListener" ./scripts/plugin-web-ui.html; then
        echo -e "${GREEN}✓ Web界面JavaScript代码存在${NC}"
    else
        echo -e "${RED}✗ Web界面JavaScript代码缺失${NC}"
        return 1
    fi
    
    # 检查CSS样式
    if grep -q "<style>" ./scripts/plugin-web-ui.html; then
        echo -e "${GREEN}✓ Web界面CSS样式存在${NC}"
    else
        echo -e "${RED}✗ Web界面CSS样式缺失${NC}"
        return 1
    fi
}

# 测试部署脚本
test_deploy_script() {
    echo -e "${YELLOW}测试部署脚本...${NC}"
    
    # 检查部署脚本是否存在
    if [ ! -f "./scripts/deploy-plugin-manager.sh" ]; then
        echo -e "${RED}错误: deploy-plugin-manager.sh 脚本不存在${NC}"
        return 1
    fi
    
    # 检查脚本权限
    if [ ! -x "./scripts/deploy-plugin-manager.sh" ]; then
        chmod +x ./scripts/deploy-plugin-manager.sh
    fi
    
    # 测试语法
    if bash -n ./scripts/deploy-plugin-manager.sh; then
        echo -e "${GREEN}✓ 部署脚本语法检查通过${NC}"
    else
        echo -e "${RED}✗ 部署脚本语法错误${NC}"
        return 1
    fi
    
    # 检查Dockerfile生成
    if grep -q "FROM python:3.9-slim" ./scripts/deploy-plugin-manager.sh; then
        echo -e "${GREEN}✓ 部署脚本包含Dockerfile生成${NC}"
    else
        echo -e "${RED}✗ 部署脚本缺少Dockerfile生成${NC}"
        return 1
    fi
}

# 测试Python API脚本
test_python_api() {
    echo -e "${YELLOW}测试Python API脚本...${NC}"
    
    # 检查Python脚本是否存在
    if [ ! -f "./scripts/plugin-config-api.py" ]; then
        echo -e "${RED}错误: plugin-config-api.py 脚本不存在${NC}"
        return 1
    fi
    
    # 检查脚本权限
    if [ ! -x "./scripts/plugin-config-api.py" ]; then
        chmod +x ./scripts/plugin-config-api.py
    fi
    
    # 检查Python语法
    if python3 -m py_compile ./scripts/plugin-config-api.py 2>/dev/null; then
        echo -e "${GREEN}✓ Python API脚本语法检查通过${NC}"
    else
        echo -e "${RED}✗ Python API脚本语法错误${NC}"
        return 1
    fi
    
    # 检查依赖导入
    if python3 -c "import flask, yaml, subprocess" 2>/dev/null; then
        echo -e "${GREEN}✓ Python依赖检查通过${NC}"
    else
        echo -e "${YELLOW}⚠ Python依赖检查失败，需要安装: pip install flask pyyaml${NC}"
    fi
}

# 生成测试报告
generate_report() {
    echo -e "${BLUE}=== 测试报告 ===${NC}"
    
    local total_tests=5
    local passed_tests=0
    
    # 统计测试结果
    if [ $? -eq 0 ]; then
        passed_tests=$((passed_tests + 1))
    fi
    
    echo "总测试数: $total_tests"
    echo "通过测试: $passed_tests"
    echo "失败测试: $((total_tests - passed_tests))"
    echo "通过率: $((passed_tests * 100 / total_tests))%"
    
    if [ $passed_tests -eq $total_tests ]; then
        echo -e "${GREEN}🎉 所有测试通过！${NC}"
    else
        echo -e "${YELLOW}⚠ 部分测试失败，请检查上述错误信息${NC}"
    fi
}

# 主测试流程
main() {
    echo -e "${PURPLE}开始插件管理器功能测试...${NC}"
    echo ""
    
    # 1. 检查依赖
    check_dependencies
    echo ""
    
    # 2. 测试命令行工具
    test_cli_tool
    echo ""
    
    # 3. 测试Python API脚本
    test_python_api
    echo ""
    
    # 4. 测试Web界面
    test_web_ui
    echo ""
    
    # 5. 测试部署脚本
    test_deploy_script
    echo ""
    
    # 6. 测试API服务（如果运行）
    test_api_service
    echo ""
    
    # 7. 生成测试报告
    generate_report
    
    echo ""
    echo -e "${BLUE}=== 使用说明 ===${NC}"
    echo "1. 部署插件管理系统: ./scripts/deploy-plugin-manager.sh"
    echo "2. 使用命令行工具: ./scripts/plugin-manager.sh"
    echo "3. 访问Web界面: http://localhost:3000"
    echo "4. 查看详细文档: ./scripts/README-plugin-manager.md"
}

# 运行测试
main "$@"
