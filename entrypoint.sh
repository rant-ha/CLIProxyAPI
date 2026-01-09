#!/bin/sh

# 1. 动态生成配置文件
# 如果没有 config.yaml，从示例复制一份
if [ ! -f config.yaml ]; then
    cp config.example.yaml config.yaml
fi

# 2. 适配 Heroku 端口
# 将配置文件中的默认端口 8317 替换为 Heroku 动态分配的 $PORT
sed -i "s/port: 8317/port: $PORT/g" config.yaml

# 3. 启动程序
# 注意：不使用 Postgres，改用 Git 存储（通过环境变量配置）
exec server
