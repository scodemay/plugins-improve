#!/bin/bash
# 部署插件管理系统脚本
# 包括API服务、Web界面和必要的Kubernetes资源

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m'

# 配置
NAMESPACE="plugin-manager"
API_SERVICE_NAME="plugin-config-api"
WEB_SERVICE_NAME="plugin-web-ui"
API_PORT=8080
WEB_PORT=3000

# 检查端口是否被占用
check_port() {
    local port=$1
    if command -v netstat >/dev/null 2>&1; then
        if netstat -tuln | grep -q ":$port "; then
            return 1  # 端口被占用
        fi
    elif command -v ss >/dev/null 2>&1; then
        if ss -tuln | grep -q ":$port "; then
            return 1  # 端口被占用
        fi
    elif command -v lsof >/dev/null 2>&1; then
        if lsof -i :$port >/dev/null 2>&1; then
            return 1  # 端口被占用
        fi
    fi
    return 0  # 端口可用
}

# 查找可用端口
find_available_port() {
    local start_port=$1
    local port=$start_port
    
    while ! check_port $port; do
        port=$((port + 1))
        if [ $port -gt $((start_port + 100)) ]; then
            echo -e "${RED}错误: 无法找到可用端口 (从 $start_port 开始)${NC}"
            exit 1
        fi
    done
    
    echo $port
}

# 推送镜像到控制平面节点
push_image_to_nodes() {
    local image_name=$1
    echo -e "${YELLOW}镜像 $image_name 已构建完成，将使用 imagePullPolicy: IfNotPresent${NC}"
    echo -e "${YELLOW}请确保镜像在Kubernetes节点上可用${NC}"
}

echo -e "${BLUE}=== 部署Kubernetes调度器插件管理系统 ===${NC}"
echo ""

# 检查依赖
check_dependencies() {
    echo -e "${YELLOW}检查依赖...${NC}"
    
    if ! command -v kubectl >/dev/null 2>&1; then
        echo -e "${RED}错误: kubectl 未安装或不在 PATH 中${NC}"
        exit 1
    fi
    
    if ! command -v docker >/dev/null 2>&1; then
        echo -e "${RED}错误: docker 未安装或不在 PATH 中${NC}"
        exit 1
    fi
    
    if ! kubectl cluster-info >/dev/null 2>&1; then
        echo -e "${RED}错误: 无法连接到 Kubernetes 集群${NC}"
        exit 1
    fi
    
    echo -e "${GREEN}✓ 依赖检查完成${NC}"
}

# 创建命名空间
create_namespace() {
    echo -e "${YELLOW}创建命名空间...${NC}"
    
    if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
        echo -e "${YELLOW}命名空间 $NAMESPACE 已存在${NC}"
    else
        kubectl create namespace "$NAMESPACE"
        echo -e "${GREEN}✓ 命名空间 $NAMESPACE 创建成功${NC}"
    fi
}

# 构建API服务镜像
build_api_image() {
    echo -e "${YELLOW}构建API服务镜像...${NC}"
    
    # 创建Dockerfile
    cat > /tmp/Dockerfile.api << 'EOF'
FROM python:3.9-slim

WORKDIR /app

# 安装系统依赖
RUN apt-get update && apt-get install -y \
    curl \
    && rm -rf /var/lib/apt/lists/*

# 安装kubectl
RUN curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" \
    && chmod +x kubectl \
    && mv kubectl /usr/local/bin/

# 安装Python依赖
RUN pip install flask flask-cors pyyaml

# 复制脚本
COPY scripts/plugin-config-api.py .

# 设置权限
RUN chmod +x plugin-config-api.py

# 暴露端口
EXPOSE 8080

# 启动命令
CMD ["python", "plugin-config-api.py"]
EOF

    # 构建镜像
    docker build -f /tmp/Dockerfile.api -t plugin-config-api:latest .
    
    echo -e "${GREEN}✓ API服务镜像构建完成${NC}"
}

# 构建Web界面镜像
build_web_image() {
    echo -e "${YELLOW}构建Web界面镜像...${NC}"
    
    # 创建Dockerfile
    cat > /tmp/Dockerfile.web << 'EOF'
FROM nginx:alpine

# 复制HTML文件
COPY scripts/plugin-web-ui.html /usr/share/nginx/html/index.html

# 创建nginx配置
RUN echo 'server { \
    listen 3000; \
    server_name localhost; \
    location / { \
        root /usr/share/nginx/html; \
        index index.html; \
    } \
    location /api/ { \
        proxy_pass http://plugin-config-api:8080/api/; \
        proxy_set_header Host $host; \
        proxy_set_header X-Real-IP $remote_addr; \
    } \
}' > /etc/nginx/conf.d/default.conf

EXPOSE 3000

CMD ["nginx", "-g", "daemon off;"]
EOF

    # 构建镜像
    docker build -f /tmp/Dockerfile.web -t plugin-web-ui:latest .
    
    echo -e "${GREEN}✓ Web界面镜像构建完成${NC}"
}

# 创建API服务部署
create_api_deployment() {
    echo -e "${YELLOW}创建API服务部署...${NC}"
    
    cat > /tmp/api-deployment.yaml << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $API_SERVICE_NAME
  namespace: $NAMESPACE
  labels:
    app: plugin-config-api
spec:
  replicas: 1
  selector:
    matchLabels:
      app: plugin-config-api
  template:
    metadata:
      labels:
        app: plugin-config-api
    spec:
      serviceAccountName: plugin-manager-sa
      containers:
      - name: api
        image: plugin-config-api:latest
        imagePullPolicy: Never
        ports:
        - containerPort: 8080
        env:
        - name: KUBERNETES_NAMESPACE
          value: "kube-system"
        - name: CONFIGMAP_NAME
          value: "rescheduler-config"
        - name: SCHEDULER_DEPLOYMENT
          value: "rescheduler-scheduler"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 200m
            memory: 256Mi
        livenessProbe:
          httpGet:
            path: /api/v1/health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /api/v1/health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: $API_SERVICE_NAME
  namespace: $NAMESPACE
  labels:
    app: plugin-config-api
spec:
  selector:
    app: plugin-config-api
  ports:
  - port: 8080
    targetPort: 8080
    protocol: TCP
  type: ClusterIP
EOF

    kubectl apply -f /tmp/api-deployment.yaml
    echo -e "${GREEN}✓ API服务部署完成${NC}"
}

# 创建Web界面部署
create_web_deployment() {
    echo -e "${YELLOW}创建Web界面部署...${NC}"
    
    cat > /tmp/web-deployment.yaml << EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $WEB_SERVICE_NAME
  namespace: $NAMESPACE
  labels:
    app: plugin-web-ui
spec:
  replicas: 1
  selector:
    matchLabels:
      app: plugin-web-ui
  template:
    metadata:
      labels:
        app: plugin-web-ui
    spec:
      containers:
      - name: web
        image: plugin-web-ui:latest
        imagePullPolicy: Never
        ports:
        - containerPort: 3000
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 100m
            memory: 128Mi
        livenessProbe:
          httpGet:
            path: /
            port: 3000
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /
            port: 3000
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: $WEB_SERVICE_NAME
  namespace: $NAMESPACE
  labels:
    app: plugin-web-ui
spec:
  selector:
    app: plugin-web-ui
  ports:
  - port: 3000
    targetPort: 3000
    protocol: TCP
  type: NodePort
EOF

    kubectl apply -f /tmp/web-deployment.yaml
    echo -e "${GREEN}✓ Web界面部署完成${NC}"
}

# 创建RBAC资源
create_rbac() {
    echo -e "${YELLOW}创建RBAC资源...${NC}"
    
    cat > /tmp/rbac.yaml << EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: plugin-manager-sa
  namespace: $NAMESPACE
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: plugin-manager-role
rules:
- apiGroups: [""]
  resources: ["configmaps", "pods", "nodes"]
  verbs: ["get", "list", "watch", "update", "patch"]
- apiGroups: ["apps"]
  resources: ["deployments", "replicasets"]
  verbs: ["get", "list", "watch", "update", "patch"]
- apiGroups: [""]
  resources: ["events"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: plugin-manager-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: plugin-manager-role
subjects:
- kind: ServiceAccount
  name: plugin-manager-sa
  namespace: $NAMESPACE
EOF

    kubectl apply -f /tmp/rbac.yaml
    echo -e "${GREEN}✓ RBAC资源创建完成${NC}"
}

# 等待部署完成
wait_for_deployment() {
    echo -e "${YELLOW}等待部署完成...${NC}"
    
    # 等待API服务
    echo "等待API服务启动..."
    kubectl wait --for=condition=available --timeout=120s deployment/$API_SERVICE_NAME -n $NAMESPACE
    
    # 等待Web界面
    echo "等待Web界面启动..."
    kubectl wait --for=condition=available --timeout=120s deployment/$WEB_SERVICE_NAME -n $NAMESPACE
    
    echo -e "${GREEN}✓ 所有服务启动完成${NC}"
}

# 显示访问信息
show_access_info() {
    echo -e "${BLUE}=== 部署完成 ===${NC}"
    echo ""
    
    # 获取NodePort
    local web_nodeport=$(kubectl get service $WEB_SERVICE_NAME -n $NAMESPACE -o jsonpath='{.spec.ports[0].nodePort}')
    local api_nodeport=$(kubectl get service $API_SERVICE_NAME -n $NAMESPACE -o jsonpath='{.spec.ports[0].nodePort}')
    
    echo -e "${GREEN}访问信息:${NC}"
    echo "Web界面 (NodePort): http://localhost:$web_nodeport"
    echo "API服务 (NodePort): http://localhost:$api_nodeport"
    echo ""
    
    echo -e "${YELLOW}端口转发命令:${NC}"
    echo "kubectl port-forward -n $NAMESPACE service/$WEB_SERVICE_NAME $WEB_PORT:3000"
    echo "kubectl port-forward -n $NAMESPACE service/$API_SERVICE_NAME $API_PORT:8080"
    echo ""
    
    echo -e "${GREEN}本地访问地址:${NC}"
    echo "Web界面: http://localhost:$WEB_PORT"
    echo "API服务: http://localhost:$API_PORT"
    echo ""
    
    echo -e "${YELLOW}管理命令:${NC}"
    echo "查看服务状态: kubectl get pods -n $NAMESPACE"
    echo "查看服务日志: kubectl logs -n $NAMESPACE -l app=plugin-config-api"
    echo "删除部署: kubectl delete namespace $NAMESPACE"
    echo ""
    
    echo -e "${BLUE}开始端口转发...${NC}"
    echo "在另一个终端运行以下命令来访问Web界面:"
    echo "kubectl port-forward -n $NAMESPACE service/$WEB_SERVICE_NAME $WEB_PORT:3000"
}

# 清理临时文件
cleanup() {
    echo -e "${YELLOW}清理临时文件...${NC}"
    rm -f /tmp/Dockerfile.api /tmp/Dockerfile.web
    rm -f /tmp/api-deployment.yaml /tmp/web-deployment.yaml
    rm -f /tmp/rbac.yaml
    echo -e "${GREEN}✓ 清理完成${NC}"
}

# 主函数
main() {
    echo -e "${PURPLE}开始部署插件管理系统...${NC}"
    
    # 1. 检查依赖
    check_dependencies
    
    # 2. 检查端口占用并调整
    echo -e "${YELLOW}检查端口占用情况...${NC}"
    if ! check_port $WEB_PORT; then
        echo -e "${YELLOW}端口 $WEB_PORT 被占用，正在查找可用端口...${NC}"
        WEB_PORT=$(find_available_port $WEB_PORT)
        echo -e "${GREEN}✓ 使用端口 $WEB_PORT${NC}"
    else
        echo -e "${GREEN}✓ 端口 $WEB_PORT 可用${NC}"
    fi
    
    if ! check_port $API_PORT; then
        echo -e "${YELLOW}端口 $API_PORT 被占用，正在查找可用端口...${NC}"
        API_PORT=$(find_available_port $API_PORT)
        echo -e "${GREEN}✓ 使用端口 $API_PORT${NC}"
    else
        echo -e "${GREEN}✓ 端口 $API_PORT 可用${NC}"
    fi
    
    # 3. 创建命名空间
    create_namespace
    
    # 4. 构建镜像
    build_api_image
    build_web_image
    
    # 5. 创建RBAC资源
    create_rbac
    
    # 6. 创建部署
    create_api_deployment
    create_web_deployment
    
    # 7. 等待部署完成
    wait_for_deployment
    
    # 8. 显示访问信息
    show_access_info
    
    # 9. 清理临时文件
    cleanup
    
    echo -e "${GREEN}🎉 插件管理系统部署完成！${NC}"
}

# 信号处理
trap 'echo -e "\n${YELLOW}部署被中断${NC}"; cleanup; exit 1' INT TERM

# 运行主程序
main "$@"
