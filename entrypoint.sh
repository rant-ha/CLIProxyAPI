#!/bin/sh

# 1. 准备配置文件
# 如果 config.yaml 不存在，从示例文件复制
if [ ! -f config.yaml ]; then
    echo "Creating config.yaml from example..."
    cp config.example.yaml config.yaml
fi

# 2. 强制修改端口 (关键步骤)
# 使用正则表达式匹配以 "port:" 开头的行，强制替换为 Heroku 分配的 $PORT
sed -i "s/^port:.*/port: $PORT/" config.yaml

# 3. 强制绑定所有网络接口 (host)
# Heroku 要求监听 0.0.0.0，而不是 localhost
sed -i 's/^host:.*/host: "0.0.0.0"/' config.yaml

# 4. 调试输出 (部署后查看日志确认)
echo "Heroku assigned PORT: $PORT"
echo "Final config setting:"
grep "^port:" config.yaml
grep "^host:" config.yaml

# 5. 启动服务
echo "Starting server..."
exec server
