# MiniNAS

一套端侧 APP + 轻量服务端部署系统，可在任意家用硬件（NAS、迷你主机、工作站甚至旧笔记本）上快速搭建私有 MiniNAS，实现手机等终端数据的自动同步、持久保存与跨设备管理。

## 面向谁

- 对数据隐私敏感，不愿将个人文件完全托管在商业云盘的用户
- 端侧数据（照片、文档、聊天记录等）量大，手机存储空间频繁告急的人
- 经常更换设备，需要无缝迁移历史数据的学生、职场人或数码爱好者

## 主要功能

- **一键式服务端部署** – 在目标家用设备上运行部署脚本或镜像，自动完成 MiniNAS 服务启动，无需复杂配置。
- **智能端侧同步** – APP 可设定文件夹自动备份，断点续传，增量同步，释放手机空间的同时保持数据完整。
- **多维数据浏览与管理** – 在 APP 内按时间、类型、来源设备筛选查看已同步文件，支持远程下载/删除/分享，真正成为私人数据中枢。

## 灵感来源

自己换手机时，总有一部分照片、文档因为云盘空间限制或忘记备份而丢失；市面私有云方案（如 Nextcloud）功能大而全，部署和维护成本很高，对部署设备性能要求也高。对于只想安静同步数据的普通家庭用户来说过于"笨重"。有没有一种方式，可以把家里闲置的旧电脑、树莓派、低端迷你主机等设备变成只做数据同步的轻量 NAS？

## 想解决的问题

- **端侧空间焦虑**：手机不敢删照片，懒得手动搬移，存储空间越用越满。
- **数据丢失风险**：换机、刷机、误删导致珍贵数据一去不返。
- **隐私与可控性**：商业云盘明文存储、账号被盗、服务关停等问题让人不安。
- **部署门槛**：现有开源方案要求熟悉 Docker、数据库、域名等，劝退非技术用户。

## 为什么做这个方向

追求 **极致轻量 + 零学习成本**。

裁剪掉网盘、在线编辑、社交等非核心功能，只保留"同步中枢"这个刚需。将部署流程简化成一条命令或一个安装包，让任何有家用电脑的人都能拥有自己的私有数据同步节点。同时围绕"端侧减负"设计 APP，让每一次清理手机空间都像把东西放进自家抽屉一样自然。

---

## 技术栈

### 服务端
- **语言**：Go 1.22
- **框架**：Chi (HTTP Router)
- **数据库**：SQLite（轻量级嵌入式数据库，无需额外部署）
- **构建工具**：Make

### 端侧
- **Android**：Kotlin + Jetpack
- **Web 管理端**：Svelte + Vite

### 容器化
- **Docker**：提供单容器和多容器两种部署方式
- **Docker Compose**：一键启动服务端和 Web 管理端

---

## 服务端部署方式

### 一键部署（推荐）

只需一条命令，即可在任意支持 Docker 的设备上完成部署：

```bash
# 克隆项目
git clone https://github.com/zhangjh/mini-nas.git
cd mini-nas

# 一键启动（首次运行会自动构建镜像）
docker compose up -d
```

服务启动后：
- Web 管理端：`http://localhost`
- API 服务：`http://localhost:8080`

**说明**：
- 数据存储在项目目录下的 `./data` 文件夹
- 默认端口：Web 前端 `80`，服务端 API `8080`
- 如需停止服务：`docker compose down`

---

### 其他部署方式

#### 单容器部署

使用单容器模式（Web 前端已嵌入服务端）：

```bash
docker build -t mini-nas-server -f server/Dockerfile.server .

docker run -d \
  --name mini-nas-server \
  -p 8080:8080 \
  -v /path/to/data:/data \
  -e TZ=Asia/Shanghai \
  --restart unless-stopped \
  mini-nas-server
```

访问地址：`http://localhost:8080`

#### 本地运行（开发调试）

```bash
cd server
go run ./cmd/server
```

**命令行参数**：
- `-config`：配置文件路径（默认 `config.json`）
- `-port`：监听端口
- `-data`：数据目录路径

#### 构建可执行文件

```bash
make build-server
./server/mini-nas-server
```

---

## 配置说明

服务端配置文件位于 `config.json`：

```json
{
  "port": 8080,
  "data_dir": "./data",
  "upload_dir": "data/files",
  "jwt_secret": "your-secret-key"
}
```

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `port` | 服务监听端口 | 8080 |
| `data_dir` | 数据存储根目录 | ./data |
| `upload_dir` | 上传文件存储目录 | data/files |
| `jwt_secret` | JWT 认证密钥 | 自动生成的随机密钥 |

---

## API 接口

### 公共接口（无需认证）

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/health` | GET | 健康检查 |

### 认证接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/auth/login` | POST | 用户登录 |
| `/api/auth/register` | POST | 用户注册 |
| `/api/auth/status` | GET | 登录状态 |

### 文件管理接口（需认证）

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/files` | GET | 文件列表 |
| `/api/files/tree` | GET | 文件树 |
| `/api/files/info` | GET | 文件信息 |
| `/api/files/search` | GET | 文件搜索 |
| `/api/files/upload` | POST | 文件上传 |
| `/api/files/download` | GET | 文件下载 |
| `/api/files/preview` | GET | 文件预览 |
| `/api/thumb` | GET | 缩略图 |
| `/api/mkdir` | POST | 创建目录 |
| `/api/file/rename` | PUT | 文件重命名 |
| `/api/file/move` | POST | 文件移动 |
| `/api/file/copy` | POST | 文件复制 |
| `/api/file/delete` | DELETE | 文件删除 |

### 系统接口（需认证）

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/system/dirs` | GET | 系统目录列表 |

### 挂载点接口（需认证）

| 接口 | 方法 | 说明 |
|------|------|------|
| `/api/mounts` | GET | 挂载点列表 |
| `/api/mounts` | POST | 创建挂载点 |
| `/api/mounts` | PUT | 更新挂载点 |
| `/api/mounts` | DELETE | 删除挂载点 |

---

## 项目结构

```
mini-nas/
├── android/          # Android 客户端
│   └── app/          # APP 源码
├── server/           # 服务端
│   ├── cmd/          # 入口文件
│   ├── internal/     # 内部模块
│   │   ├── api/      # API 路由与处理器
│   │   ├── config/   # 配置管理
│   │   ├── model/    # 数据模型
│   │   └── service/  # 业务逻辑
│   └── Dockerfile    # Docker 构建文件
├── web/              # Web 管理端
│   ├── src/          # 前端源码
│   └── Dockerfile    # Docker 构建文件
├── scripts/          # 脚本工具
├── docker-compose.yml # 容器编排
├── Makefile          # 构建脚本
└── config.json       # 默认配置
```

---

## 开发指南

### 启动开发环境

```bash
# 启动服务端（终端1）
make dev-server

# 启动 Web 前端（终端2）
make dev-web
```

### 构建生产版本

```bash
# 构建所有组件
make build

# 构建服务端
make build-server

# 构建 Web 前端
make build-web
```

---

## 许可证

MIT License（待添加 LICENSE 文件）

---

## 贡献

欢迎提交 Issue 和 Pull Request！

---

## 注意事项

1. **数据安全**：请定期备份 `./data` 目录，以防数据丢失
2. **网络安全**：建议在局域网环境内使用，如需公网访问，请配置防火墙和 HTTPS
3. **存储权限**：`docker-compose.yml` 中挂载了宿主机根目录 `/:/host`，用于支持访问宿主机文件系统。请确保部署环境安全，避免恶意访问
4. **性能考虑**：对于大量小文件同步，建议使用 SSD 存储以获得更好的性能