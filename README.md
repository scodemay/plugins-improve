# Scheduler-Plugins

A repository of scheduler plugins built on top of the Kubernetes Scheduler Framework.

[![CI](https://github.com/kubernetes-sigs/scheduler-plugins/actions/workflows/ci.yaml/badge.svg)](https://github.com/kubernetes-sigs/scheduler-plugins/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/sigs.k8s.io/scheduler-plugins)](https://goreportcard.com/report/sigs.k8s.io/scheduler-plugins)

## Overview

This repository contains a collection of scheduler plugins that extend the capabilities of the Kubernetes default scheduler. These plugins implement advanced scheduling algorithms for workload optimization, resource efficiency, and cluster performance enhancement.

### Key Features

- **Production-Ready Plugins**: Battle-tested scheduling algorithms for enterprise environments
- **Framework Integration**: Built on the official Kubernetes Scheduler Framework
- **Performance Monitoring**: Comprehensive metrics and observability stack
- **Flexible Deployment**: Support for both secondary and primary scheduler configurations
- **Enterprise Support**: Advanced features for multi-tenant and high-performance computing environments

## Supported Plugins

| Plugin | Description | Use Cases | Maturity |
|--------|-------------|-----------|----------|
| **CoScheduling** | Gang scheduling for tightly coupled workloads | ML/AI training, HPC, distributed databases | Stable |
| **Rescheduler** | Dynamic load balancing and workload optimization | Production clusters, resource efficiency | Stable |
| **CapacityScheduling** | Resource quota-aware scheduling with elasticity | Multi-tenant environments, resource governance | Stable |
| **NodeResourceTopology** | NUMA-aware and topology-conscious scheduling | High-performance workloads, latency-sensitive apps | Beta |
| **Trimaran** | Real-time load-aware scheduling | Dynamic workloads, auto-scaling environments | Alpha |

## Prerequisites

### System Requirements

- **Kubernetes**: v1.28.0 or higher
- **Go**: 1.21+ (for development)
- **Docker**: 20.10+ (for image building)
- **kubectl**: Compatible with your cluster version

### Resource Requirements

- **CPU**: 2+ cores for development, 4+ cores for production
- **Memory**: 4GB+ for development, 8GB+ for production
- **Storage**: 20GB+ available disk space

### Supported Platforms

- Linux (amd64, arm64, s390x, ppc64le)
- Container runtimes: Docker, containerd, CRI-O

## Quick Start

### Installation Methods

#### Option 1: Helm Chart (Recommended for Testing)

```bash
# Add the scheduler-plugins Helm repository
helm repo add scheduler-plugins https://kubernetes-sigs.github.io/scheduler-plugins
helm repo update

# Install as a secondary scheduler
helm install scheduler-plugins scheduler-plugins/as-a-second-scheduler \
  --namespace scheduler-plugins \
  --create-namespace
```

#### Option 2: Manifest Deployment

```bash
# Deploy the scheduler-plugins controller and RBAC
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/scheduler-plugins/master/manifests/install/all-in-one.yaml

# Verify deployment
kubectl get pods -n scheduler-plugins
```

#### Option 3: Build from Source

```bash
# Clone the repository
git clone https://github.com/kubernetes-sigs/scheduler-plugins.git
cd scheduler-plugins

# Build binaries
make build

# Build container images
make local-image

# Deploy to cluster
kubectl apply -f manifests/install/all-in-one.yaml
```

## Rescheduler Plugin Configuration

The Rescheduler plugin provides intelligent workload rebalancing and scheduling optimization. This section details how to configure and deploy the Rescheduler plugin as a standalone scheduler.

### Architecture Overview

The Rescheduler plugin operates in two modes:

1. **Scheduling Mode**: Optimizes initial pod placement based on cluster load
2. **Rescheduling Mode**: Continuously monitors and rebalances existing workloads

### Key Algorithms

- **Load Balancing Score**: Calculates optimal node placement based on resource utilization
- **Preventive Rescheduling**: Proactively moves workloads before resource exhaustion
- **Multi-Resource Scoring**: Weighted scoring across CPU, memory, and custom metrics

### Configuration Parameters

```yaml
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: rescheduler-scheduler
  plugins:
    score:
      enabled:
      - name: Rescheduler
  pluginConfig:
  - name: Rescheduler
    args:
      # Resource utilization thresholds
      cpuThreshold: 80.0              # CPU threshold (%)
      memoryThreshold: 80.0           # Memory threshold (%)
      
      # Scheduling optimization
      enableSchedulingOptimization: true
      enablePreventiveRescheduling: true
      
      # Scoring weights
      cpuScoreWeight: 0.6             # CPU weight in scoring
      memoryScoreWeight: 0.4          # Memory weight in scoring
      loadBalanceBonus: 10.0          # Load balance bonus score
      
      # Rescheduling controller
      enableReschedulingController: true
      reschedulingInterval: "30s"     # Rescheduling check interval
      
      # Namespace exclusions
      excludedNamespaces:
      - kube-system
      - kube-public
```

### Deploying Rescheduler Plugin

#### Step 1: Deploy Prerequisites

```bash
# Create priority class for scheduler
kubectl apply -f manifests/rescheduler/priority-class.yaml

# Create RBAC resources
kubectl apply -f manifests/rescheduler/rbac.yaml
```

#### Step 2: Configure the Scheduler

```bash
# Apply scheduler configuration
kubectl apply -f manifests/rescheduler/configmap.yaml

# Deploy the scheduler
kubectl apply -f manifests/rescheduler/deployment.yaml
```

#### Step 3: Apply Pod Eviction Permissions (if needed)

```bash
# Apply additional permissions for pod eviction
kubectl apply -f fix-rescheduler-permissions.yaml
```

#### Step 4: Verify Deployment

```bash
# Check scheduler status
kubectl get pods -n kube-system -l app=rescheduler-scheduler

# View scheduler logs
kubectl logs -n kube-system -l app=rescheduler-scheduler -f

# Expected output:
# I0101 00:00:00.000000       1 rescheduler.go:123] "Rescheduler plugin initialized"
# I0101 00:00:00.000000       1 rescheduler.go:456] "Starting rescheduling controller"
```

### Using the Rescheduler Scheduler

To use the Rescheduler plugin, specify the scheduler name in your workload manifests:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: example-app
spec:
  replicas: 10
  selector:
    matchLabels:
      app: example-app
  template:
    metadata:
      labels:
        app: example-app
    spec:
      schedulerName: rescheduler-scheduler  # Use Rescheduler plugin
      containers:
      - name: app
        image: nginx:latest
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
```

### Advanced Configuration

#### Tuning Rescheduling Behavior

```yaml
# Custom configuration for high-frequency rescheduling
pluginConfig:
- name: Rescheduler
  args:
    cpuThreshold: 70.0              # Lower threshold for more aggressive rescheduling
    memoryThreshold: 75.0
    reschedulingInterval: "15s"     # More frequent checks
    enablePreventiveRescheduling: true
    cpuScoreWeight: 0.7             # Higher CPU weight
    memoryScoreWeight: 0.3
```

#### Production Recommendations

```yaml
# Production-optimized configuration
pluginConfig:
- name: Rescheduler
  args:
    cpuThreshold: 85.0              # Conservative thresholds
    memoryThreshold: 85.0
    reschedulingInterval: "60s"     # Longer intervals for stability
    enablePreventiveRescheduling: false  # Disable for critical workloads
    excludedNamespaces:
    - kube-system
    - kube-public
    - monitoring
    - critical-apps
```

## Monitoring and Observability

### Deploying the Monitoring Stack

```bash
# Deploy Prometheus, Grafana, and metrics collectors
./tools/monitoring/deploy-enhanced-monitoring.sh

# Access monitoring interfaces
kubectl port-forward -n monitoring svc/grafana-service 3000:3000 &
kubectl port-forward -n monitoring svc/prometheus-service 9090:9090 &
```

### Key Metrics

The scheduler plugins expose the following metrics:

- `scheduler_plugin_execution_duration_seconds`: Plugin execution time
- `rescheduler_pod_movements_total`: Number of pod rescheduling operations
- `rescheduler_load_balance_score`: Cluster load balance score
- `rescheduler_node_utilization`: Per-node resource utilization

### Grafana Dashboards

Pre-configured dashboards are available in `monitoring/configs/`:

- **Cluster Load Balance**: Overall cluster balance and efficiency metrics
- **Scheduler Performance**: Plugin performance and latency metrics
- **Rescheduling Activity**: Pod movement and optimization statistics

## Performance Testing

### Running Benchmarks

```bash
# Execute comprehensive performance tests
./scripts/run-performance-tests.sh

# Monitor performance in real-time
./scripts/monitor-performance.sh 300  # Monitor for 5 minutes
```

### Test Scenarios

The test suite includes:

- **Baseline Scheduling**: Standard Kubernetes scheduler comparison
- **Load Balancing**: Workload distribution effectiveness
- **Resource Pressure**: High-utilization scenario handling
- **Scalability**: Large cluster performance characteristics

## Troubleshooting

### Common Issues

#### Scheduler Pod CrashLoopBackOff

```bash
# Check scheduler logs
kubectl logs -n kube-system -l app=rescheduler-scheduler

# Common causes:
# - Missing RBAC permissions
# - Invalid configuration
# - Image pull failures

# Verify RBAC
kubectl auth can-i get pods --as=system:serviceaccount:kube-system:rescheduler-scheduler
```

#### Pods Not Being Rescheduled

```bash
# Check rescheduling controller logs
kubectl logs -n kube-system -l app=rescheduler-scheduler | grep -i rescheduling

# Verify pod eviction permissions
kubectl auth can-i create pods/eviction --as=system:serviceaccount:kube-system:rescheduler-scheduler

# Apply additional permissions if needed
kubectl apply -f fix-rescheduler-permissions.yaml
```

#### High Resource Usage

```bash
# Monitor scheduler resource consumption
kubectl top pods -n kube-system -l app=rescheduler-scheduler

# Adjust resource limits in deployment
kubectl patch deployment rescheduler-scheduler -n kube-system -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-scheduler","resources":{"limits":{"cpu":"1000m","memory":"1Gi"}}}]}}}}'
```

### Debug Mode

Enable verbose logging for troubleshooting:

```bash
# Edit scheduler deployment
kubectl patch deployment rescheduler-scheduler -n kube-system -p '{"spec":{"template":{"spec":{"containers":[{"name":"kube-scheduler","args":["--config=/etc/kubernetes/config.yaml","--v=4"]}]}}}}'
```

## Development

### Building and Testing

```bash
# Install dependencies
go mod download

# Run unit tests
make unit-test

# Run integration tests
make integration-test

# Build binaries
make build

# Build images
make local-image
```

### Code Organization

```
pkg/
├── capacityscheduling/    # Capacity scheduling plugin
├── coscheduling/          # Gang scheduling plugin
├── rescheduler/           # Load balancing plugin
├── noderesourcetopology/  # NUMA-aware plugin
└── trimaran/              # Load-aware plugins
```

### Contributing

1. Fork the repository
2. Create a feature branch
3. Implement your changes with tests
4. Submit a pull request

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed guidelines.

## Production Deployment

### High Availability Setup

For production environments, deploy the scheduler in HA mode:

```yaml
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 1
```

### Security Considerations

- Use least-privilege RBAC configurations
- Enable audit logging for scheduler decisions
- Implement resource quotas and limits
- Regular security scanning of container images

### Performance Tuning

- Adjust plugin priorities based on workload characteristics
- Configure appropriate resource limits and requests
- Monitor and tune garbage collection settings
- Optimize etcd performance for large clusters

## API Reference

### Scheduler Configuration

The scheduler configuration follows the Kubernetes Scheduler Configuration API:

- [KubeSchedulerConfiguration v1](https://kubernetes.io/docs/reference/config-api/kube-scheduler-config.v1/)
- [Plugin Configuration Reference](https://kubernetes.io/docs/reference/scheduling/config/)

### Plugin-Specific APIs

Each plugin exposes its own configuration parameters. See the respective plugin documentation:

- [Rescheduler Configuration](pkg/rescheduler/README.md)
- [CoScheduling Configuration](pkg/coscheduling/README.md)
- [CapacityScheduling Configuration](pkg/capacityscheduling/README.md)

## Support and Community

### Getting Help

- **GitHub Issues**: [Report bugs and request features](https://github.com/kubernetes-sigs/scheduler-plugins/issues)
- **Slack**: Join `#sig-scheduling` on Kubernetes Slack
- **Mailing List**: kubernetes-sig-scheduling@googlegroups.com

### Community Resources

- **Weekly Meetings**: SIG Scheduling meetings (see community calendar)
- **Documentation**: [Official documentation site](https://scheduler-plugins.sigs.k8s.io/)
- **Examples**: Sample configurations in `manifests/` directory

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Kubernetes SIG Scheduling community
- Scheduler Framework contributors
- Plugin developers and maintainers

---

For more information, visit the [official documentation](https://scheduler-plugins.sigs.k8s.io/) or join our community discussions.