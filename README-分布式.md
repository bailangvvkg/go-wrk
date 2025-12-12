```127.0.0.1:890
# 默认
docker run --rm \
--network host \
bailangvvking/go-wrk:distributed /go-wrk -c 10 -d 10 http://example.com
# 协调器
docker run \
--network host \
--rm bailangvvking/go-wrk:distributed \
/go-wrk-coordinator -port 8080
# 工作节点1
docker run --rm \
--network host \
bailangvvking/go-wrk:distributed \
/go-wrk-worker -port 8081 -id worker1
# 分布式
docker run --rm \
--network host \
bailangvvking/go-wrk:distributed /go-wrk-dist -c 100 -d 30 \
-coordinator "http://协调器:8080" \
  -workers "客户机1:8081,客户机2:8081" \
  http://127.0.0.1:80
```





# go-wrk 分布式压测系统

这是一个基于 `go-wrk` 的分布式压测扩展，可以跨多台机器运行压力测试，突破单机带宽限制，生成更高的负载。

## 架构设计

分布式系统包含三个主要组件：

1. **协调器 (Coordinator)** - 中央节点，负责任务分发和结果汇总
2. **工作节点 (Worker)** - 多个工作节点，执行实际的压测任务
3. **客户端 (Client)** - 命令行工具，用于发起分布式测试

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   客户端    │────▶│   协调器    │────▶│  工作节点1  │
└─────────────┘     └─────────────┘     └─────────────┘
                           │                    │
                           │                    │
                           ▼                    ▼
                    ┌─────────────┐     ┌─────────────┐
                    │   结果汇总   │◀────│  工作节点N  │
                    │             │     └─────────────┘
                    └─────────────┘
```

## 安装

### 编译所有组件

```bash
# 编译协调器
go build -o go-wrk-coordinator coordinator.go

# 编译工作节点
go build -o go-wrk-worker worker.go

# 编译分布式客户端
go build -o go-wrk-dist go-wrk-dist.go

# 编译原始go-wrk（可选，用于对比）
go build -o go-wrk go-wrk.go
```

## 使用方法

### 1. 启动协调器

在协调器机器上（通常是本地机器或中央服务器）：

```bash
./go-wrk-coordinator -port 8080
```

### 2. 启动工作节点

在每个工作节点机器上（可以是多台机器或同一机器的多个实例）：

```bash
# 在工作节点1上（使用不同端口）
./go-wrk-worker -port 8081 -id worker1

# 在工作节点2上
./go-wrk-worker -port 8082 -id worker2

# 在工作节点3上（同一机器，不同端口）
./go-wrk-worker -port 8083 -id worker3
```

### 3. 运行分布式压测

使用分布式客户端工具：

```bash
# 基础测试，2个工作节点
./go-wrk-dist -c 100 -d 30 \
  -workers "192.168.1.100:8081,192.168.1.101:8082" \
  http://example.com

# 高级测试，自定义头部和请求体
./go-wrk-dist -c 50 -d 60 \
  -coordinator "http://协调器主机:8080" \
  -workers "worker1:8081,worker2:8081,worker3:8081" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer token123" \
  -body '{"测试": "数据"}' \
  -M POST \
  https://api.example.com/端点
```

## 命令行参数

### 协调器参数
```
-port      协调器HTTP端口（默认：8080）
```

### 工作节点参数
```
-port      工作节点HTTP端口（默认：8081）
-id        工作节点ID（默认：主机名）
```

### 分布式客户端参数
支持所有标准 `go-wrk` 参数，额外增加：

```
-coordinator   协调器URL（默认：http://localhost:8080）
-workers       逗号分隔的工作节点列表（主机:端口）
               （默认：localhost:8081）
```

标准go-wrk参数：
```
-c        每个工作节点的goroutine数量（默认：10）
-d        测试持续时间（秒）（默认：10）
-H        添加到每个请求的头部（可多次使用）
-M        HTTP方法（默认：GET）
-T        套接字/请求超时（毫秒）（默认：1000）
-body     请求体字符串或@文件名
-redir    允许重定向（默认：false）
-no-c     禁用压缩
-no-ka    禁用KeepAlive
-no-vr    跳过SSL证书验证
-cert     CA证书文件
-key      私钥文件名
-ca       用于验证对等方的CA文件
-http     使用HTTP/2（默认：true）
```

## API接口

### 协调器API
- `POST /start` - 启动新的分布式压测
- `GET /status` - 获取当前测试状态
- `GET /results` - 获取聚合的测试结果
- `POST /worker/report` - 工作节点报告结果（内部使用）

### 工作节点API
- `POST /start` - 在此工作节点上启动压测
- `POST /stop` - 停止当前压测
- `GET /status` - 获取工作节点状态
- `GET /health` - 健康检查端点

## 性能优势

### 突破带宽限制
单机限制：
- 网络接口卡（NIC）带宽（1Gbps、10Gbps等）
- TCP连接限制
- CPU和内存约束

使用分布式测试：
- 聚合多台机器的带宽
- 跨节点分发连接负载
- 通过添加更多工作节点水平扩展

### 扩展示例
假设每个工作节点可以生成：
- 10,000 请求/秒
- 100MB/秒 带宽

使用10个工作节点：
- **100,000 请求/秒** 总量
- **1GB/秒** 总带宽
- 随工作节点数量线性扩展

## 部署场景

### 1. 本地测试（多端口）
```bash
# 终端1：协调器
./go-wrk-coordinator -port 8080

# 终端2：工作节点1
./go-wrk-worker -port 8081 -id worker1

# 终端3：工作节点2
./go-wrk-worker -port 8082 -id worker2

# 终端4：运行测试
./go-wrk-dist -c 100 -d 30 -workers "localhost:8081,localhost:8082" http://localhost:8080
```

### 2. 跨机器测试
```bash
# 在协调器机器上 (192.168.1.50)
./go-wrk-coordinator -port 8080

# 在工作节点1机器上 (192.168.1.100)
./go-wrk-worker -port 8081 -id worker1

# 在工作节点2机器上 (192.168.1.101)
./go-wrk-worker -port 8081 -id worker2

# 在客户端机器上
./go-wrk-dist -c 200 -d 60 \
  -coordinator "http://192.168.1.50:8080" \
  -workers "192.168.1.100:8081,192.168.1.101:8081" \
  http://目标服务.com
```

### 3. 云部署
在云实例（AWS EC2、GCP VM等）上部署工作节点，协调器部署在中央服务器。

## 监控和结果

### 实时状态
```bash
# 检查协调器状态
curl http://localhost:8080/status

# 检查工作节点状态
curl http://工作节点主机:8081/status
```

### 最终结果
结果包括：
- 所有工作节点的总请求数
- 总错误数和错误分布
- 聚合请求速率（请求/秒）
- 聚合传输速率（MB/秒）
- 延迟百分位数（p50、p90、p99等）
- 每个工作节点的统计信息

## 最佳实践

1. **工作节点放置**：将工作节点放在靠近目标服务的位置，最小化网络延迟
2. **协调器位置**：协调器可以在任何可以访问工作节点网络的地方
3. **扩展策略**：从2-3个工作节点开始，根据需要添加更多
4. **监控**：测试期间监控工作节点的CPU、内存和网络使用情况
5. **安全性**：在生产环境中使用HTTPS进行协调器-工作节点通信

## 当前限制和未来改进

### 当前限制
- 基于HTTP的简单通信（无加密）
- 基础错误处理
- 无持久化任务队列
- 手动部署工作节点

### 计划改进
- [ ] 协调器-工作节点通信的TLS加密
- [ ] 基于Web的监控仪表板
- [ ] 自动化工作节点发现
- [ ] 支持不同的负载模式（逐渐增加、峰值等）
- [ ] 与现有监控系统集成

## 故障排除

### 常见问题

1. **工作节点未报告结果**
   - 检查工作节点和协调器之间的网络连接
   - 验证工作节点可以访问协调器URL
   - 检查工作节点日志中的错误

2. **请求率低**
   - 增加每个工作节点的goroutine数量（`-c`参数）
   - 添加更多工作节点
   - 检查目标服务容量

3. **连接错误**
   - 调整超时值（`-T`参数）
   - 检查工作节点上的DNS解析
   - 验证目标服务可以从工作节点访问

### 调试模式
通过设置环境变量为协调器和工作节点添加详细日志：
```bash
export GO_WRK_DEBUG=1
./go-wrk-coordinator -port 8080
```

## 与原始go-wrk的比较

| 功能 | go-wrk | go-wrk分布式版 |
|------|--------|---------------|
| 单机测试 | ✓ | ✓（通过工作节点） |
| 多机测试 | ✗ | ✓ |
| 带宽扩展 | 受限于单机NIC | 所有工作节点的聚合带宽 |
| 最大连接数 | 受限于单机操作系统 | 所有工作节点的总和 |
| 部署 | 单个二进制文件 | 协调器 + 多个工作节点 |
| 结果聚合 | 单机统计 | 跨机器聚合统计 |

## 如何贡献

欢迎贡献！请随时提交Pull Request。

## 许可证

与原始go-wrk相同 - 参见LICENSE文件。
