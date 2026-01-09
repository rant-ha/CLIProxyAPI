#!/bin/sh

# 1. 设置数据库连接
# Heroku 会自动提供 DATABASE_URL，我们将其赋值给程序需要的 PGSTORE_DSN
export PGSTORE_DSN=$DATABASE_URL

# 2. 动态生成配置文件
# 如果没有 config.yaml，从示例复制一份
if [ ! -f config.yaml ]; then
    cp config.example.yaml config.yaml
fi

# 3. 适配 Heroku 端口
# 将配置文件中的默认端口 8317 替换为 Heroku 动态分配的 $PORT
sed -i "s/port: 8317/port: $PORT/g" config.yaml

# 4. 启动程序
# 注意：Heroku Go Buildpack 默认会将 cmd/server 编译为名为 "server" 的二进制文件
exec server
