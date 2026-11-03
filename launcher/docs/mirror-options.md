# 镜像与依赖下载来源

## 界面策略

设置保留“使用镜像下载”开关和三个第三方公共 GitHub 代理：

- `https://ghproxy.net/`
- `https://ghfast.top/`
- `https://gh-proxy.com/`

关闭为 `official`，仅官方直连；开启默认 `mirror`，仅镜像；也可选择 `auto`，官方失败后尝试镜像。保存后生效，修改运行配置须先停止服务。代理前缀用于公开 Release 元数据及固定提交源码归档，不要求用户填写签名公钥。

这些服务不是影策官方、不是已承诺的中国内地节点，也不保证大文件稳定、无限带宽或完整离线安装。不发送 Token、Cookie、私有仓库凭据或项目内容；选择代理意味着接受其元数据与传输信任边界。摘要检查不等于独立发布者签名。

## 源码构建还有其他网络链路

| 对象 | 当前行为 |
| --- | --- |
| main 提交解析 API / GitHub 源码归档 | 按 official、mirror、auto 来源策略 |
| Go 模块 | official 用 `https://proxy.golang.org,direct`；非 official 用 `https://goproxy.cn` |
| Bun/npm 依赖 | official 用 `https://registry.npmjs.org`；非 official 用 `https://registry.npmmirror.com` |
| 缺失 Go / Node / Bun / Windows LLVM 工具 | 固定官方工具站或官方 GitHub 发布，要求官方 SHA-256；不因启用 GitHub 代理就保证全部工具下载可达 |

因此 `auto` 也会为构建依赖启用镜像配置，不只是 GitHub 失败时切代理。所有缓存位于受管目录，不改用户全局包源、PATH、代理或证书。

当前探测：官方源码归档约 300 MB 已下载，受管 Bun 验证成功；官方直连 Go 依赖连接失败，镜像 Go 依赖成功。完整首次安装仍在验证，不能据此声称三个代理、全部工具或 Agent/npm 链路均通过。

## 失败与尚未完成

切换源后重试保留明确失败阶段及受控日志。完整源码归档缓存可复用，但尚无部分文件断点续传、下载取消或磁盘空间预检。Windows CGO 路径修正后仍待完整重试结果，macOS/Linux 尚未原生验收。

目前不需要购买云账号或生成公钥。自建镜像、正式操作系统签名/公证及长期服务保证是独立发行工作，未创建服务或向远端发布。旧签名脚本不属于当前安装路径。
