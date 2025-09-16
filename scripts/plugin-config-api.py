#!/usr/bin/env python3
"""
Kubernetes调度器插件配置API服务
提供RESTful接口进行插件的实时启用、禁用和配置管理
"""

import os
import json
import yaml
import logging
from datetime import datetime
from typing import Dict, List, Optional, Any
from flask import Flask, request, jsonify, abort
from flask_cors import CORS
import subprocess
import tempfile

# 配置日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

app = Flask(__name__)
CORS(app)

# 配置
NAMESPACE = os.getenv('KUBERNETES_NAMESPACE', 'kube-system')
CONFIGMAP_NAME = os.getenv('CONFIGMAP_NAME', 'rescheduler-config')
SCHEDULER_DEPLOYMENT = os.getenv('SCHEDULER_DEPLOYMENT', 'rescheduler-scheduler')

# 支持的插件和阶段
SUPPORTED_PLUGINS = [
    "Rescheduler",
    "Coscheduling", 
    "CapacityScheduling",
    "NodeResourceTopologyMatch",
    "NodeResourcesAllocatable",
    "TargetLoadPacking",
    "LoadVariationRiskBalancing",
    "PreemptionToleration",
    "PodState",
    "QoS",
    "SySched",
    "Trimaran"
]

PLUGIN_PHASES = [
    "filter",
    "score", 
    "reserve",
    "preBind",
    "preFilter",
    "postFilter",
    "permit",
    "bind",
    "postBind"
]

class PluginConfigManager:
    """插件配置管理器"""
    
    def __init__(self):
        self.namespace = NAMESPACE
        self.configmap_name = CONFIGMAP_NAME
        self.scheduler_deployment = SCHEDULER_DEPLOYMENT
    
    def get_current_config(self) -> Dict[str, Any]:
        """获取当前配置"""
        try:
            cmd = [
                'kubectl', 'get', 'configmap', self.configmap_name,
                '-n', self.namespace, '-o', 'json'
            ]
            result = subprocess.run(cmd, capture_output=True, text=True, check=True)
            config_data = json.loads(result.stdout)
            
            # 解析YAML配置
            yaml_config = yaml.safe_load(config_data['data']['config.yaml'])
            return yaml_config
        except subprocess.CalledProcessError as e:
            logger.error(f"获取配置失败: {e.stderr}")
            raise Exception(f"获取配置失败: {e.stderr}")
        except Exception as e:
            logger.error(f"解析配置失败: {e}")
            raise Exception(f"解析配置失败: {e}")
    
    def update_config(self, new_config: Dict[str, Any]) -> bool:
        """更新配置"""
        try:
            # 备份当前配置
            self.backup_config()
            
            # 转换为YAML
            yaml_content = yaml.dump(new_config, default_flow_style=False)
            
            # 更新ConfigMap
            cmd = [
                'kubectl', 'patch', 'configmap', self.configmap_name,
                '-n', self.namespace, '--type', 'merge',
                '-p', json.dumps({
                    'data': {
                        'config.yaml': yaml_content
                    }
                })
            ]
            
            result = subprocess.run(cmd, capture_output=True, text=True, check=True)
            logger.info("配置更新成功")
            return True
            
        except subprocess.CalledProcessError as e:
            logger.error(f"更新配置失败: {e.stderr}")
            return False
        except Exception as e:
            logger.error(f"更新配置失败: {e}")
            return False
    
    def backup_config(self):
        """备份当前配置"""
        try:
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            backup_file = f"/tmp/config_backup_{timestamp}.yaml"
            
            cmd = [
                'kubectl', 'get', 'configmap', self.configmap_name,
                '-n', self.namespace, '-o', 'yaml'
            ]
            
            with open(backup_file, 'w') as f:
                subprocess.run(cmd, stdout=f, check=True)
            
            logger.info(f"配置已备份到: {backup_file}")
            
        except Exception as e:
            logger.warning(f"配置备份失败: {e}")
    
    def restart_scheduler(self) -> bool:
        """重启调度器"""
        try:
            cmd = [
                'kubectl', 'rollout', 'restart', f'deployment/{self.scheduler_deployment}',
                '-n', self.namespace
            ]
            
            subprocess.run(cmd, check=True)
            
            # 等待重启完成
            cmd = [
                'kubectl', 'rollout', 'status', f'deployment/{self.scheduler_deployment}',
                '-n', self.namespace, '--timeout=300s'
            ]
            
            subprocess.run(cmd, check=True)
            logger.info("调度器重启成功")
            return True
            
        except subprocess.CalledProcessError as e:
            logger.error(f"重启调度器失败: {e.stderr}")
            return False
        except Exception as e:
            logger.error(f"重启调度器失败: {e}")
            return False
    
    def enable_plugin(self, plugin_name: str, phases: List[str]) -> Dict[str, Any]:
        """启用插件"""
        if plugin_name not in SUPPORTED_PLUGINS:
            raise ValueError(f"不支持的插件: {plugin_name}")
        
        for phase in phases:
            if phase not in PLUGIN_PHASES:
                raise ValueError(f"不支持的阶段: {phase}")
        
        config = self.get_current_config()
        
        # 确保profiles结构存在
        if 'profiles' not in config:
            config['profiles'] = [{'schedulerName': 'rescheduler-scheduler', 'plugins': {}}]
        
        profile = config['profiles'][0]
        if 'plugins' not in profile:
            profile['plugins'] = {}
        
        # 为每个阶段添加插件
        for phase in phases:
            if phase not in profile['plugins']:
                profile['plugins'][phase] = {'enabled': [], 'disabled': []}
            
            if 'enabled' not in profile['plugins'][phase]:
                profile['plugins'][phase]['enabled'] = []
            if 'disabled' not in profile['plugins'][phase]:
                profile['plugins'][phase]['disabled'] = []
            
            # 添加到enabled列表
            if plugin_name not in profile['plugins'][phase]['enabled']:
                profile['plugins'][phase]['enabled'].append(plugin_name)
            
            # 从disabled列表移除
            if plugin_name in profile['plugins'][phase]['disabled']:
                profile['plugins'][phase]['disabled'].remove(plugin_name)
        
        # 更新配置
        if self.update_config(config):
            self.restart_scheduler()
            return {
                'status': 'success',
                'message': f'插件 {plugin_name} 在阶段 {phases} 中启用成功',
                'plugin': plugin_name,
                'phases': phases
            }
        else:
            return {
                'status': 'error',
                'message': f'插件 {plugin_name} 启用失败'
            }
    
    def disable_plugin(self, plugin_name: str, phases: List[str]) -> Dict[str, Any]:
        """禁用插件"""
        if plugin_name not in SUPPORTED_PLUGINS:
            raise ValueError(f"不支持的插件: {plugin_name}")
        
        for phase in phases:
            if phase not in PLUGIN_PHASES:
                raise ValueError(f"不支持的阶段: {phase}")
        
        config = self.get_current_config()
        
        # 确保profiles结构存在
        if 'profiles' not in config:
            config['profiles'] = [{'schedulerName': 'rescheduler-scheduler', 'plugins': {}}]
        
        profile = config['profiles'][0]
        if 'plugins' not in profile:
            profile['plugins'] = {}
        
        # 为每个阶段禁用插件
        for phase in phases:
            if phase not in profile['plugins']:
                profile['plugins'][phase] = {'enabled': [], 'disabled': []}
            
            if 'enabled' not in profile['plugins'][phase]:
                profile['plugins'][phase]['enabled'] = []
            if 'disabled' not in profile['plugins'][phase]:
                profile['plugins'][phase]['disabled'] = []
            
            # 从enabled列表移除
            if plugin_name in profile['plugins'][phase]['enabled']:
                profile['plugins'][phase]['enabled'].remove(plugin_name)
            
            # 添加到disabled列表
            if plugin_name not in profile['plugins'][phase]['disabled']:
                profile['plugins'][phase]['disabled'].append(plugin_name)
        
        # 更新配置
        if self.update_config(config):
            self.restart_scheduler()
            return {
                'status': 'success',
                'message': f'插件 {plugin_name} 在阶段 {phases} 中禁用成功',
                'plugin': plugin_name,
                'phases': phases
            }
        else:
            return {
                'status': 'error',
                'message': f'插件 {plugin_name} 禁用失败'
            }
    
    def update_plugin_config(self, plugin_name: str, config_key: str, config_value: Any) -> Dict[str, Any]:
        """更新插件配置"""
        config = self.get_current_config()
        
        # 确保profiles结构存在
        if 'profiles' not in config:
            config['profiles'] = [{'schedulerName': 'rescheduler-scheduler', 'pluginConfig': []}]
        
        profile = config['profiles'][0]
        if 'pluginConfig' not in profile:
            profile['pluginConfig'] = []
        
        # 查找插件配置
        plugin_config = None
        for pc in profile['pluginConfig']:
            if pc.get('name') == plugin_name:
                plugin_config = pc
                break
        
        if plugin_config is None:
            # 创建新的插件配置
            plugin_config = {'name': plugin_name, 'args': {}}
            profile['pluginConfig'].append(plugin_config)
        
        # 更新配置值
        if 'args' not in plugin_config:
            plugin_config['args'] = {}
        
        plugin_config['args'][config_key] = config_value
        
        # 更新配置
        if self.update_config(config):
            self.restart_scheduler()
            return {
                'status': 'success',
                'message': f'插件 {plugin_name} 配置 {config_key} 更新为 {config_value}',
                'plugin': plugin_name,
                'config': {config_key: config_value}
            }
        else:
            return {
                'status': 'error',
                'message': f'插件 {plugin_name} 配置更新失败'
            }
    
    def get_plugin_status(self) -> Dict[str, Any]:
        """获取插件状态"""
        config = self.get_current_config()
        
        status = {
            'enabled_plugins': {},
            'disabled_plugins': {},
            'plugin_configs': {}
        }
        
        if 'profiles' in config and len(config['profiles']) > 0:
            profile = config['profiles'][0]
            
            # 获取启用的插件
            if 'plugins' in profile:
                for phase, phase_config in profile['plugins'].items():
                    if 'enabled' in phase_config:
                        status['enabled_plugins'][phase] = phase_config['enabled']
                    if 'disabled' in phase_config:
                        status['disabled_plugins'][phase] = phase_config['disabled']
            
            # 获取插件配置
            if 'pluginConfig' in profile:
                for pc in profile['pluginConfig']:
                    status['plugin_configs'][pc['name']] = pc.get('args', {})
        
        return status

# 创建管理器实例
config_manager = PluginConfigManager()

# API路由
@app.route('/api/v1/plugins', methods=['GET'])
def get_plugins():
    """获取所有插件状态"""
    try:
        status = config_manager.get_plugin_status()
        return jsonify({
            'status': 'success',
            'data': status,
            'supported_plugins': SUPPORTED_PLUGINS,
            'supported_phases': PLUGIN_PHASES
        })
    except Exception as e:
        logger.error(f"获取插件状态失败: {e}")
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 500

@app.route('/api/v1/plugins/<plugin_name>/enable', methods=['POST'])
def enable_plugin(plugin_name):
    """启用插件"""
    try:
        data = request.get_json() or {}
        phases = data.get('phases', ['filter', 'score'])
        
        if not isinstance(phases, list):
            phases = [phases]
        
        result = config_manager.enable_plugin(plugin_name, phases)
        return jsonify(result)
    except ValueError as e:
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 400
    except Exception as e:
        logger.error(f"启用插件失败: {e}")
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 500

@app.route('/api/v1/plugins/<plugin_name>/disable', methods=['POST'])
def disable_plugin(plugin_name):
    """禁用插件"""
    try:
        data = request.get_json() or {}
        phases = data.get('phases', ['filter', 'score'])
        
        if not isinstance(phases, list):
            phases = [phases]
        
        result = config_manager.disable_plugin(plugin_name, phases)
        return jsonify(result)
    except ValueError as e:
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 400
    except Exception as e:
        logger.error(f"禁用插件失败: {e}")
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 500

@app.route('/api/v1/plugins/<plugin_name>/config', methods=['PUT'])
def update_plugin_config(plugin_name):
    """更新插件配置"""
    try:
        data = request.get_json()
        if not data:
            return jsonify({
                'status': 'error',
                'message': '请求体不能为空'
            }), 400
        
        results = []
        for key, value in data.items():
            result = config_manager.update_plugin_config(plugin_name, key, value)
            results.append(result)
        
        return jsonify({
            'status': 'success',
            'message': f'插件 {plugin_name} 配置更新完成',
            'results': results
        })
    except Exception as e:
        logger.error(f"更新插件配置失败: {e}")
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 500

@app.route('/api/v1/scheduler/restart', methods=['POST'])
def restart_scheduler():
    """重启调度器"""
    try:
        if config_manager.restart_scheduler():
            return jsonify({
                'status': 'success',
                'message': '调度器重启成功'
            })
        else:
            return jsonify({
                'status': 'error',
                'message': '调度器重启失败'
            }), 500
    except Exception as e:
        logger.error(f"重启调度器失败: {e}")
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 500

@app.route('/api/v1/health', methods=['GET'])
def health_check():
    """健康检查"""
    try:
        # 检查kubectl连接
        result = subprocess.run(
            ['kubectl', 'cluster-info'], 
            capture_output=True, text=True, timeout=10
        )
        
        if result.returncode == 0:
            return jsonify({
                'status': 'healthy',
                'message': '服务正常运行',
                'timestamp': datetime.now().isoformat()
            })
        else:
            return jsonify({
                'status': 'unhealthy',
                'message': 'Kubernetes连接异常'
            }), 503
    except Exception as e:
        logger.error(f"健康检查失败: {e}")
        return jsonify({
            'status': 'unhealthy',
            'message': str(e)
        }), 503

@app.route('/api/v1/config/backup', methods=['POST'])
def backup_config():
    """备份当前配置"""
    try:
        config_manager.backup_config()
        return jsonify({
            'status': 'success',
            'message': '配置备份成功'
        })
    except Exception as e:
        logger.error(f"配置备份失败: {e}")
        return jsonify({
            'status': 'error',
            'message': str(e)
        }), 500

# 错误处理
@app.errorhandler(404)
def not_found(error):
    return jsonify({
        'status': 'error',
        'message': '接口不存在'
    }), 404

@app.errorhandler(500)
def internal_error(error):
    return jsonify({
        'status': 'error',
        'message': '服务器内部错误'
    }), 500

if __name__ == '__main__':
    # 检查依赖
    try:
        subprocess.run(['kubectl', 'version'], check=True, capture_output=True)
        logger.info("Kubernetes连接正常")
    except subprocess.CalledProcessError:
        logger.error("无法连接到Kubernetes集群")
        exit(1)
    except FileNotFoundError:
        logger.error("kubectl命令未找到")
        exit(1)
    
    # 启动服务
    port = int(os.getenv('PORT', 8080))
    host = os.getenv('HOST', '0.0.0.0')
    
    logger.info(f"启动插件配置API服务: {host}:{port}")
    app.run(host=host, port=port, debug=False)
