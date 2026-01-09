#!/bin/sh

# 1. 准备基础配置文件
if [ ! -f config.yaml ]; then
    echo "Creating config.yaml from example..."
    cp config.example.yaml config.yaml
fi

# 2. 修改端口和监听地址 (这是 Heroku 必须的)
# 使用更宽松的正则，确保能替换成功
sed -i "s/port: *[0-9]*/port: $PORT/" config.yaml
sed -i 's/host: *".*"/host: "0.0.0.0"/' config.yaml

# 3. 【关键步骤】将配置文件复制到程序的默认读取路径
# 程序很可能只读 ~/.cli-proxy-api/config.yaml，不读当前目录
mkdir -p "$HOME/.cli-proxy-api"
cp config.yaml "$HOME/.cli-proxy-api/config.yaml"

# 4. 再次确认一下文件内容 (调试用)
echo "=== Debug Info ==="
echo "Heroku PORT: $PORT"
echo "Checking config file at $HOME/.cli-proxy-api/config.yaml:"
grep "port:" "$HOME/.cli-proxy-api/config.yaml"
echo "=================="

# 5. 启动程序
exec server
