# SunCodexClaw

把飞书消息接到本地或容器中的 Codex CLI，执行任务后再把结果、进度和附件回写飞书。

当前推荐的部署模型很简单：

- 业务配置只认两个 TOML 文件
- 默认本机直接运行
- 只有显式加 `--docker-compose` 时才走 Docker Compose
- 如果你想把“本机模式”写得更明确，也可以显式加 `--local`
- 本机模式下，`start/preflight` 不带 `--account` 时默认处理所有 `enabled = true` 的机器人
- 本机模式下，`status/stop` 不带 `--account` 时默认处理所有已配置机器人，包括 `enabled = false` 但仍有状态残留的机器人
- 本机模式下，`restart` 不带 `--account` 时会先停止所有已配置机器人，再只启动 `enabled = true` 的机器人
- Compose 模式下，`start/status/stop/restart/logs` 操作的是整个 `suncodexclaw` 容器服务，不按单个账号筛选
- `list/configure/timer/memory/env/clawhub/sync` 也支持 `--docker-compose`
- `workspace-docs refresh` 也支持 `--docker-compose`
- `list` 会列出所有已配置机器人及其 `enabled/disabled` 状态
- `timer/memory/env/sync` 这类账号作用域命令在仓库根目录下建议显式带 `--account`，在机器人工作目录中可依赖 `.config.toml` 自动判定

## 你需要准备

- Go 1.22+（如果你要本地编译 `suncodexclawd`）
- Docker / Docker Compose（仅当你要用容器部署时）
- 一个飞书企业自建应用
- 可用的 Codex / OpenAI 凭据

飞书应用至少需要满足：

- 启用机器人能力
- 事件与回调使用“长连接”
- 订阅事件 `im.message.receive_v1`
- 每次修改订阅配置后发布应用版本

## 飞书应用权限

当前项目实测可用的一套完整权限清单如下。

- 这是“当前实测可用的完整权限集合”，不是严格收敛后的最小权限集合
- 新部署建议先整套导入，跑通后再按需收缩

<details>
<summary>展开查看完整 scopes</summary>

```json
{
  "scopes": {
    "tenant": [
      "aily:file:read",
      "aily:file:write",
      "application:application.app_message_stats.overview:readonly",
      "application:application:self_manage",
      "application:bot.menu:write",
      "cardkit:card:write",
      "contact:contact.base:readonly",
      "contact:user.employee_id:readonly",
      "corehr:file:download",
      "docs:document.content:read",
      "docx:document",
      "docx:document.block:convert",
      "docx:document:create",
      "docx:document:readonly",
      "docx:document:write_only",
      "drive:drive",
      "drive:drive.metadata:readonly",
      "drive:drive.search:readonly",
      "drive:drive:readonly",
      "drive:drive:version",
      "drive:drive:version:readonly",
      "event:ip_list",
      "im:app_feed_card:write",
      "im:biz_entity_tag_relation:read",
      "im:biz_entity_tag_relation:write",
      "im:chat",
      "im:chat.access_event.bot_p2p_chat:read",
      "im:chat.announcement:read",
      "im:chat.announcement:write_only",
      "im:chat.chat_pins:read",
      "im:chat.chat_pins:write_only",
      "im:chat.collab_plugins:read",
      "im:chat.collab_plugins:write_only",
      "im:chat.managers:write_only",
      "im:chat.members:bot_access",
      "im:chat.members:read",
      "im:chat.members:write_only",
      "im:chat.menu_tree:read",
      "im:chat.menu_tree:write_only",
      "im:chat.moderation:read",
      "im:chat.tabs:read",
      "im:chat.tabs:write_only",
      "im:chat.top_notice:write_only",
      "im:chat.widgets:read",
      "im:chat.widgets:write_only",
      "im:chat:create",
      "im:chat:delete",
      "im:chat:moderation:write_only",
      "im:chat:operate_as_owner",
      "im:chat:read",
      "im:chat:readonly",
      "im:chat:update",
      "im:datasync.feed_card.time_sensitive:write",
      "im:message",
      "im:message.group_at_msg:readonly",
      "im:message.group_msg",
      "im:message.p2p_msg:readonly",
      "im:message.pins:read",
      "im:message.pins:write_only",
      "im:message.reactions:read",
      "im:message.reactions:write_only",
      "im:message.urgent",
      "im:message.urgent.status:write",
      "im:message.urgent:phone",
      "im:message.urgent:sms",
      "im:message:readonly",
      "im:message:recall",
      "im:message:send_as_bot",
      "im:message:send_multi_depts",
      "im:message:send_multi_users",
      "im:message:send_sys_msg",
      "im:message:update",
      "im:resource",
      "im:tag:read",
      "im:tag:write",
      "im:url_preview.update",
      "im:user_agent:read",
      "sheets:spreadsheet",
      "wiki:wiki:readonly"
    ],
    "user": [
      "aily:file:read",
      "aily:file:write",
      "im:chat.access_event.bot_p2p_chat:read"
    ]
  }
}
```

</details>

## 配置结构

SunCodexClaw 现在只认这两个业务配置文件：

- `config/feishu/bots.toml`
  - 共享运行配置放在 `[shared]`
  - 每个机器人自己的非敏感覆盖项放在 `[bot.<account>]`
  - 如果账号名里包含 `.`、空格等特殊字符，手工编辑时应写成 `[bot."your account"]`
  - 每个机器人可单独配置 `enabled = true|false`，默认 `true`
- `config/secrets/local.toml`
  - 飞书凭据、Codex/OpenAI 密钥、WebDAV 等敏感配置
  - 同理，账号名包含特殊字符时，使用 `[feishu."your account"]`、`[sync."your account"]`

优先级规则：

- 对某个机器人，如果 `bots.toml` 和 `local.toml` 都配置了同名字段，优先使用该机器人的私有配置
- 典型写法是：敏感项放 `local.toml`，运行行为和机器人差异放 `bots.toml`

示例：

```toml
# config/feishu/bots.toml
[shared]
domain = "feishu"
reply_mode = "codex"
reply_prefix = "AI 助手："
require_mention = true
require_mention_group_only = true

[shared.codex]
cwd_root = "workspace"
model = "gpt-5.4"

[bot.assistant]
enabled = true
bot_name = "飞书 Codex 助手"
```

```toml
# config/secrets/local.toml
[feishu.assistant]
app_id = "cli_xxx"
app_secret = "your_app_secret"
encrypt_key = "your_encrypt_key"
verification_token = "your_verification_token"

[feishu.assistant.codex]
api_key = "sk-xxxx"
# 自定义 codex.base_url 必须支持 /v1/responses 的 WebSocket 握手。
base_url = "https://api.openai.com/v1"
```

## 快速开始

### 1. 拉取仓库

```bash
git clone https://github.com/rainoffallingstar/SunCodexClaw.git
cd SunCodexClaw
```

### 2. 准备目录和模板

```bash
mkdir -p .codex .runtime workspace config/feishu config/secrets
cp .env.example .env
cp config/feishu/bots.example.toml config/feishu/bots.toml
cp config/secrets/local.example.toml config/secrets/local.toml
```

说明：

- `.env` 只给 `docker compose` 做变量替换，不承载业务配置
- 默认共享工作区根目录建议写成相对路径 `workspace`
- 如果只配置了 `shared.codex.cwd_root = "workspace"`，每个机器人会自动派生自己的工作目录 `workspace/<account-namespace>`
- `<account-namespace>` 是账号名按本地目录规则清洗后的结果，例如空格和斜杠会转成 `-`
- 这样同一份配置可以同时兼容本机模式和 Docker Compose 模式

### 3. 初始化配置

```bash
suncodexclawd configure --account <account>
```

追加第二个机器人：

```bash
suncodexclawd configure add --account <another-account>
```

编辑已有机器人：

```bash
suncodexclawd configure edit --account <account>
```

`configure` 的行为：

- 直接对 `config/feishu/bots.toml` 与 `config/secrets/local.toml` 做交互式 add/edit
- 已有值会作为默认值回显，直接回车即可保留
- 使用 `--yes` 时会自动接受当前值或推荐默认值，适合快速初始化
- 不再生成容器内配置文件
- 不再从环境变量导入业务配置
- 如果你更习惯宿主机只保留 Compose，也可以执行 `suncodexclawd configure --docker-compose --account <account>`

### 4. 预检查

本机模式：

```bash
suncodexclawd preflight --account <account>
```

容器模式：

```bash
suncodexclawd preflight --docker-compose --account <account>
```

### 5. 启动

默认本机直接运行：

```bash
suncodexclawd start
suncodexclawd logs --account <account> -f
```

如果你要用 Docker Compose，显式加 `--docker-compose`：

```bash
suncodexclawd start --docker-compose
suncodexclawd status --docker-compose
suncodexclawd logs --docker-compose -f
```

说明：

- `start/restart/status/stop/logs/preflight` 默认都按本机模式执行
- 当前只保留 Go native 飞书运行时。它已支持 dry-run、工作区初始化、WebSocket 收消息、文本/文件/图片/语音消息处理、`post` 富文本里的嵌图读取、附件回传指令、`/timer` `/memory` `/sync` `/thread` 命令、多线程本地上下文、消息级进度提示、typing reaction、fake stream、`progress.mode=doc` 的飞书文档进度页，以及 timer 任务里的附件回传
- Go native 的 `progress.mode=doc` 会创建文档、写入任务概览/用户消息/最终回复、尝试分享给当前会话并设置链接范围
- Go native 现已读取 `codex exec --json` 事件流，并把常见的 `thread/turn/item/command/raw/error` 事件写入消息进度或 doc 进度
- 本机模式下，`start/preflight` 不带 `--account` 时，会处理 `bots.toml` 中所有 `enabled = true` 的机器人
- 本机模式下，`status/stop` 不带 `--account` 时，会处理所有已配置机器人，便于发现或停止已经被禁用但仍有残留进程/日志状态的机器人
- 本机模式下，`restart` 不带 `--account` 时，会先停止所有已配置机器人，再只启动 `enabled = true` 的机器人
- 如果某个机器人暂时不想启动，直接在 `[bot.<account>]` 下设置 `enabled = false`
- `list/configure/timer/memory/env/clawhub/sync` 也支持显式 `--docker-compose`
- `workspace-docs refresh` 也支持显式 `--docker-compose`
- 这些工具型命令在 Compose 模式下会优先执行 `docker compose exec suncodexclaw suncodexclawd <subcommand> ...`
- 如果 Compose 服务还没启动，会先尝试 `docker compose pull suncodexclaw` 后再执行 `docker compose run --rm --workdir /app suncodexclaw <subcommand> ...`
- 只有拉取失败时，才会回退到 `docker compose run --rm --workdir /app --build suncodexclaw <subcommand> ...`
- 这些工具型命令进入容器后会统一使用容器内的 `/app` 作为 repo 根，不依赖宿主机路径
- `start/status/stop/restart/logs --docker-compose` 管理的是整个 Compose 服务，不支持 `--account`
- 当前 `docker-compose.yml` 同时声明了 `image` 和 `build`；因此 `start/restart/update --docker-compose` 会默认先 `docker compose pull suncodexclaw`
- 如果拉取成功，再直接启动/刷新服务；只有拉取失败时，才回退到本地 `Dockerfile` 构建
- `start --docker-compose` 会执行“先 pull，失败再 `docker compose up -d --build` 回退”的策略
- `status --docker-compose` 等价于 `docker compose ps`
- `logs --docker-compose` 等价于 `docker compose logs suncodexclaw`
- `stop --docker-compose` 等价于 `docker compose down`
- `restart --docker-compose` 会执行“先 pull，失败再 `docker compose up -d --build --force-recreate` 回退”的策略
- `update --docker-compose` 会执行同样的“pull 优先，build 回退”刷新策略
- 如果 Compose 项目不在当前目录，使用 `update --docker-compose --project-dir /path/to/SunCodexClaw`
- `update --docker-compose` 不接受二进制更新模式下的 `--repo/--version/--bin/--check/--dry-run`
- 如果本机没有 `docker`，显式使用 `--docker-compose` 会直接报错退出

## 运行模式

### 本机模式

- 默认模式
- 适合开发、调试、或不想依赖 Docker 的部署
- 由 `suncodexclawd` 直接拉起本机进程

### Docker Compose 模式

- 仅在显式使用 `--docker-compose` 时启用
- Compose 会挂载 `config/`、`.runtime/`、`workspace/`，并用独立的 Docker volume 保存容器内的 `CODEX_HOME`
- 容器内执行 `suncodexclawd start` 时仍然按默认本机模式启动，不会再套一层 Docker
- 如果 `codex.base_url` 指向 Tailscale 网络里的另一台机器，优先使用该节点可路由的 Tailscale IP 或完整 MagicDNS 名称；不要依赖 `localhost`、`host.docker.internal`，也尽量不要只写未限定域名的短主机名
- 仅“地址可达”还不够。Codex CLI 会把 `codex.base_url` 当作 Responses API 入口，并要求目标在 `/v1/responses` 支持 WebSocket 握手；如果网关只兼容普通 HTTP/OpenAI 接口，通常会报 `400 Bad Request websocket: bad handshake`
- 因此，`speech.base_url` 可以指向普通 HTTP 兼容网关，但 `codex.base_url` 必须指向官方 OpenAI，或你自己部署的、明确支持 Responses WebSocket 的网关
- 现在默认不再把宿主机仓库下的 `./.codex` 绑进容器；容器会使用自己持久化的 `CODEX_HOME` volume。如果要让 Compose 容器复用某一份现成 Codex 配置，需要手动把对应配置写入这个 volume 内
- 容器启动时现在会默认尝试执行 `suncodexclawd codex-home sync`：会为每个启用 bot 生成独立的 `HOME` 与 `CODEX_HOME`，并把各自的 `codex.base_url` / `codex.api_key` 写入对应目录下的 `config.toml` 与 `auth.json`
- 当前默认目录形如 `CODEX_HOME_ROOT/bot-homes/<account-namespace>/.codex`；运行时会为每个 bot 注入对应的 `HOME` / `CODEX_HOME`，不需要创建真实系统用户
- 这一步只是减少对环境变量注入的依赖，不能绕过 Codex CLI 仍要求 `/v1/responses` WebSocket 的限制
- 如果某个 bot 自己的 `CODEX_HOME` 里已经有不是 SunCodexClaw 托管的 `config.toml` / `auth.json`，该 bot 的同步会自动跳过并继续使用现有环境变量路径
- 如需关闭这一步，可在 `.env` 里设置 `SUNCODEXCLAW_SYNC_CODEX_HOME=false`
- 如果你之前已经创建过 `codex_home` volume，重建镜像后仍遇到 `Permission denied (os error 13)`，删除旧 volume 后再 `docker compose up -d --build`，让新 volume 继承镜像里预设的 `/home/node/.codex` 权限
- 默认会把宿主机的 `WORKSPACE_PATH` 挂到容器内 `/app/workspace`，因此推荐把 `shared.codex.cwd_root` 配成相对路径 `workspace`

`.env.example` 只包含 Compose 相关变量，例如：

```dotenv
IMAGE_REPOSITORY=rainoffallingstar/suncodexclaw
IMAGE_TAG=main
WORKSPACE_PATH=./workspace
HEALTH_PORT=8080
SUNCODEXCLAW_UID=1000
SUNCODEXCLAW_GID=1000
SUNCODEXCLAW_SYNC_CODEX_HOME=true
# CODEX_NPM_PKG=@openai/codex
```

## 多机器人

多机器人不需要额外开关，仓库会自动按配置感知。

规则：

- `config/feishu/bots.toml` 中每个 `[bot.<account>]` 代表一个机器人
- 在仓库根目录执行 `timer/memory/sync` 这类账号作用域命令时，推荐显式使用 `--account <account>`
- 如果当前 shell 已经位于某个机器人的工作目录中，可以依赖 `.config.toml` 自动识别该账号
- 这条习惯在单机器人场景下也同样适用

推荐习惯：

- `suncodexclawd start --account <account>`
- `suncodexclawd timer list --account <account>`
- `suncodexclawd memory search 中文 --account <account>`
- `suncodexclawd sync push --account <account>`

## 工作区初始化

每个机器人第一次进入自己的运行目录时，会自动完成这些动作：

- 初始化该运行目录为 Git 仓库
- 写入 `.config.toml`
- 检查 `agent.md`、`soul.md`、`heartbeats.md`
- 如果已配置 WebDAV，同步系统会先尝试 `sync restore`
- restore 没补齐的文件，才会按默认模板创建
- 如果你想无视远端当前快照、强制把工作区文档刷新成当前代码内置的最新版模板，可执行 `suncodexclawd workspace-docs refresh --account <account>`
- 自动创建当前机器人的默认文档同步任务 `workspace-doc-sync`
  - 该任务会沿用当前机器人实际生效的 `sync.workspace_id`

`.config.toml` 会记录：

- 当前机器人账号
- 当前工作目录
- 相关配置文件路径
- 当前目录对应的 `timer/memory/sync` 账号作用域
- 当前生效的 `sync_workspace_id`

## 默认生成的运行文档

每个机器人工作目录默认会维护 4 个关键文件：

- `.config.toml`
  - 机器生成，不建议手改
  - 记录当前目录绑定的机器人账号、配置文件位置、`timer/memory/sync` 作用域、文档路径等事实信息
- `agent.md`
  - 机器人在当前目录里的“操作手册”
  - 会告诉它当前目录是单机器人工作目录、哪些 `suncodexclawd` 技能可用、何时应使用 memory/timer/sync、何时需要显式 `--account`
- `soul.md`
  - 机器人的长期人格和边界模板
  - 更偏行为准则和风格，不偏运行时事实
- `heartbeats.md`
  - 轻量现场笔记
  - 记录运行环境变化、关键故障、安装的新技能、待跟进事项

默认模板已经和当前实现对齐：

- `agent.md` 会明确说明本目录绑定单一机器人账号
- `agent.md` 会说明 `timer/memory/env/sync` 在本目录可直接依赖 `.config.toml` 识别账号
- `agent.md` 会包含 `env` 与 `clawhub` 的使用入口
- `agent.md` 会说明 `list/configure/timer/memory/env/clawhub/sync` 支持 `--docker-compose`
- `agent.md` 会说明 `configure edit`、显式 `--local`、以及 `launchagents`/`update` 的使用边界
- `heartbeats.md` 会提醒记录新安装的技能

## 常用命令

本机模式：

```bash
suncodexclawd list
suncodexclawd status --local --account <account>
suncodexclawd status --account <account>
suncodexclawd logs --account <account> -f
suncodexclawd restart --account <account>
suncodexclawd configure edit --account <account>
suncodexclawd launchagents status --account <account>
suncodexclawd memory search 中文 --account <account>
suncodexclawd sync status --account <account>
suncodexclawd update --check
```

如果服务已经跑在 Compose 里，也可以直接进容器执行：

```bash
docker compose exec suncodexclaw suncodexclawd status --account <account>
docker compose logs -f suncodexclaw
docker compose exec suncodexclaw suncodexclawd memory list --account <account>
```

如果你希望统一从宿主机通过 Compose 调工具命令，也可以直接这样写：

```bash
suncodexclawd list --docker-compose
suncodexclawd configure --docker-compose --account <account>
suncodexclawd configure edit --docker-compose --account <account>
suncodexclawd timer list --docker-compose --account <account>
suncodexclawd memory search 中文 --docker-compose --account <account>
suncodexclawd env list --docker-compose --account <account>
suncodexclawd clawhub search --docker-compose "timer skill"
suncodexclawd sync status --docker-compose --account <account>
suncodexclawd workspace-docs refresh --docker-compose --account <account>
```

如果你已经位于某个机器人自己的工作目录里，也可以直接依赖 `.config.toml`：

```bash
suncodexclawd memory list
suncodexclawd env list
suncodexclawd timer list
suncodexclawd sync status
suncodexclawd workspace-docs refresh
```

## macOS 开机常驻

如果你是在 macOS 本机模式部署，并希望用 `launchd` 做开机常驻，可以使用：

```bash
suncodexclawd launchagents install --account <account>
suncodexclawd launchagents status --account <account>
suncodexclawd launchagents uninstall --account <account>
```

说明：

- `launchagents` 只服务本机模式，不走 `--docker-compose`
- 默认会按当前配置生成每个账号自己的 plist
- 如果你已经切到 Go native 运行时，也可以配合 `--run-mode supervisor` 使用本机 `suncodexclawd`

## 定时任务

内置 `timer` 子系统支持：

- `list`
- `show`
- `upsert`
- `update`
- `run`
- `logs`
- `enable`
- `disable`
- `delete`

示例：

```bash
suncodexclawd timer upsert \
  --id daily-report \
  --account <account> \
  --chat-id oc_xxx \
  --daily 09:00 \
  --tz Asia/Shanghai \
  --cwd workspace/<account-namespace> \
  --prompt "检查仓库并把日报发回当前会话"
```

更新已有任务：

```bash
suncodexclawd timer update daily-report \
  --account <account> \
  --daily 10:30 \
  --prompt "检查仓库并发送更新后的日报"
```

每个机器人第一次启动进入自己的工作目录时，会自动创建一个文档同步任务：

- 任务名：`workspace-doc-sync`
- 动作：同步 `agent.md`、`soul.md`、`heartbeats.md`
- 周期：每 24 小时一次
- 存储位置：`config/timers/<account-namespace>/workspace-doc-sync.json`
  - 这里的 `<account-namespace>` 是账号名按本地目录规则清洗后的结果，例如空格和斜杠会转成 `-`
- 如果尚未配置 WebDAV，任务会以 `--skip-if-unconfigured` 安静跳过

## 飞书里的 `/timer`

机器人支持直接在飞书里用 `/timer` 管理任务，例如：

- `/timer help`
- `/timer list`
- `/timer show daily-report`
- `/timer run daily-report`
- `/timer logs daily-report`
- `/timer enable daily-report`
- `/timer disable daily-report`
- `/timer delete daily-report`
- `/timer 创建一个每天 09:00 执行的日报任务，目录 workspace/<account-namespace>，结果发回当前会话`
- `/timer 把 daily-report 改成每天 10:30 执行，并把任务内容改成检查当前工作目录后发回当前会话`

说明：

- `/timer` 默认操作当前机器人账号的定时任务
- 用自然语言创建任务时，默认把结果发回当前会话

## 记忆系统

内置 `memory` 子系统会按机器人账号分库。

- 每个机器人使用独立记忆库
- 存储位置：`config/memory/libraries/<account-namespace>/entries/*.json`
- 适合存长期偏好、长期规则、检索线索
- 运行 Codex 前会自动检索相关长期记忆并注入上下文
- 明显的长期偏好或规则类用户消息会谨慎自动写入记忆
- 如果用户再次强调相同或近似表达的长期偏好或规则，系统会在去重的同时累积 `reinforce_count / last_reinforced_at` 并温和提升其权重
- 可通过 `kind / priority / pinned` 主动调节召回权重
- 被实际召回并参与回答的记忆会累积 `use_count / last_used_at`，后续排序会适度偏向“经常真的有用”的记忆
- 低价值旧记忆可先归档而不是直接删除；归档后默认不会参与召回、搜索、重复检测，但仍可恢复
- 在机器人工作目录里，命令行 `memory` 子命令可从 `.config.toml` 自动推断 `--account`；在仓库根目录下仍建议显式传入

常用命令：

```bash
suncodexclawd memory add --account <account> --text "以后默认用简体中文回复"
suncodexclawd memory add --account <account> --text "以后默认用简体中文回复" --force-new
suncodexclawd memory force --account <account> --text "以后默认用简体中文回复"
suncodexclawd memory force --account <account> --text "以后默认用简体中文回复" --json
suncodexclawd memory stats --account <account>
suncodexclawd memory stats --account <account> --limit 10
suncodexclawd memory stats --account <account> --json
suncodexclawd memory add --account <account> --text "以后默认用简体中文回复" --json
suncodexclawd memory list --account <account> --json
suncodexclawd memory search 中文 --account <account> --json
suncodexclawd memory review --account <account>
suncodexclawd memory review --account <account> --min-score 130 --stale-days 45
suncodexclawd memory review --account <account> --apply-promote
suncodexclawd memory review --account <account> --apply-stale
suncodexclawd memory review --account <account> --apply-all --min-score 130
suncodexclawd memory archive mem-20260320-090000-000 --account <account>
suncodexclawd memory unarchive mem-20260320-090000-000 --account <account>
suncodexclawd memory purge --account <account>
suncodexclawd memory purge --account <account> --days 45
suncodexclawd memory purge --account <account> --days 45 --apply
suncodexclawd memory related mem-20260320-090000-000 --account <account>
suncodexclawd memory related mem-20260320-090000-000 --account <account> --min-score 130
suncodexclawd memory duplicates --account <account> --min-score 100
suncodexclawd memory dedupe --account <account> --min-score 100
suncodexclawd memory dedupe --account <account> --min-score 130 --apply
suncodexclawd memory update mem-20260320-090000-000 --account <account> --kind preference --priority 80 --pinned
suncodexclawd memory pin mem-20260320-090000-000 --account <account>
suncodexclawd memory unpin mem-20260320-090000-000 --account <account>
suncodexclawd memory list --account <account> --archived
suncodexclawd memory search 中文 --account <account> --archived
suncodexclawd memory recall 中文回复 --account <account>
suncodexclawd memory recall 中文回复 --account <account> --all
suncodexclawd memory recall 中文回复 --account <account> --json
suncodexclawd memory related mem-20260320-090000-000 --account <account> --json
suncodexclawd memory duplicates --account <account> --min-score 100 --json
suncodexclawd memory dedupe --account <account> --min-score 100 --json
suncodexclawd memory dedupe --account <account> --min-score 130 --apply --json
suncodexclawd memory update mem-20260320-090000-000 --account <account> --kind preference --priority 80 --pinned --json
suncodexclawd memory pin mem-20260320-090000-000 --account <account> --json
suncodexclawd memory archive mem-20260320-090000-000 --account <account> --json
suncodexclawd memory merge mem-20260320-090000-000 mem-20260319-080000-000 mem-20260318-070000-000 --account <account>
suncodexclawd memory merge mem-20260320-090000-000 mem-20260319-080000-000 --account <account> --json
suncodexclawd memory list --account <account>
suncodexclawd memory search 中文 --account <account>
suncodexclawd memory show mem-20260320-090000-000 --account <account>
suncodexclawd memory delete mem-20260320-090000-000 --account <account>
suncodexclawd memory delete mem-20260320-090000-000 --account <account> --json
```

## 飞书里的 `/memory`

机器人支持直接在飞书里用 `/memory` 管理长期记忆，例如：

- `/memory help`
- `/memory 以后默认用简体中文回复`
- `/memory add 代码修改后默认顺手跑测试`
- `/memory add --force-new 以后默认用简体中文回复`
- `/memory force 以后默认用简体中文回复`
- `/memory list`
- `/memory review`
- `/memory review 130`
- `/memory stats`
- `/memory stats 10`
- `/memory review apply`
- `/memory review apply stale 130`
- `/memory review apply promote`
- `/memory list archived`
- `/memory list all`
- `/memory recall 中文回复`
- `/memory recall archived 中文回复`
- `/memory purge`
- `/memory purge 45`
- `/memory purge apply 45`
- `/memory archive mem-20260320-090000-000`
- `/memory unarchive mem-20260320-090000-000`
- `/memory related mem-20260320-090000-000`
- `/memory related mem-20260320-090000-000 130`
- `/memory duplicates`
- `/memory duplicates 130`
- `/memory dedupe`
- `/memory dedupe apply`
- `/memory dedupe apply 130`
- `/memory search 中文`
- `/memory search archived 中文`
- `/memory search all 中文`
- `/memory show mem-20260320-090000-000`
- `/memory pin mem-20260320-090000-000`
- `/memory unpin mem-20260320-090000-000`
- `/memory merge mem-20260320-090000-000 mem-20260319-080000-000 mem-20260318-070000-000`
- `/memory update mem-20260320-090000-000 以后默认先给结论再解释`
- `/memory delete mem-20260320-090000-000`
- `/memory remove mem-20260320-090000-000`

说明：

- `/memory` 默认操作当前机器人账号自己的独立记忆库
- `memory review` / `/memory review` 会主动汇总重复候选、值得晋升的高价值记忆、以及长期闲置的低价值 note，适合做治理体检
- `memory add` / `/memory add` 在命中高置信度重复时会优先强化已有记忆，而不是继续创建近似重复条目
- `memory force` / `/memory force` 是 `memory add --force-new` / `/memory add --force-new` 的简写
- 如果你确实需要保留一条新的近似重复记忆，可在命令行使用 `memory add --force-new` 或 `memory force`，也可在飞书里使用 `/memory add --force-new ...`、`/memory force ...`
- `memory recall` / `/memory recall` 会直接预览当前自动召回逻辑会命中的记忆排序，适合调试 active memory
- `memory recall` 的输出会附带简单命中原因，便于理解是文本匹配、priority、pinned、reinforce 还是 use_count 在起作用
- 自动 recall 遇到高度相似的重复记忆时，会优先保留排序更高的一条，并在原因里补充 `collapsed_similar:N`，避免重复规则一起污染上下文
- `memory stats` / `/memory stats` 会输出当前记忆库总览，适合先看 active/archived、kind 分布和 top memories 再决定下一步治理动作
- 除 `memory show` 默认直接输出原始 JSON 外，大部分 `memory` 子命令都支持 `--json`，方便自动化或外部面板直接消费
- `memory review --apply-promote|--apply-stale|--apply-all` 与飞书 `/memory review apply ...` 默认不会处理 duplicate 分组，只会批量执行 promote 和/或 stale->archive
- `memory archive` / `/memory archive` 会把记忆从默认召回和搜索结果里隐藏，但不会永久丢失；适合处理 stale note
- `memory purge` / `/memory purge` 默认只预览超过保留期的归档记忆；显式 apply 后才会物理删除
- 飞书里的 `/memory list archived|all` 和 `/memory search archived|all <关键词>` 可直接排查归档记忆
- `memory related` / `/memory related` 支持围绕单条记忆查看附近的近似或重复候选，适合在 merge 前做点状检查
- `memory duplicates` / `memory dedupe` 支持 `--min-score`，值越高越保守，只看更高置信度的重复候选
- 飞书里的 `/memory review 130`、`/memory related mem-xxx 130`、`/memory duplicates 130`、`/memory dedupe 130`、`/memory dedupe apply 130` 也支持同样的阈值控制
- `/memory duplicates` 会先列出疑似重复候选，适合在 merge 前做人工确认
- `/memory dedupe` 默认只预览会合并哪些分组；显式使用 `/memory dedupe apply` 或命令行 `memory dedupe --apply` 时才会真正批量合并
- `/memory pin` 可以把高价值长期规则固定到更高优先级，便于自动召回
- `/memory remove` 是 `/memory delete` 的别名，适合顺手删除低价值错误条目
- 命令行的 `memory list/search` 支持 `--archived` 查看归档记忆，也支持 `--all` 同时查看活跃和归档条目
- `/memory merge` 会保留一条主记忆，并吸收其他重复或近似条目的元数据后删除它们

## 环境变量库

内置 `env` 子系统用于按 `global` 或机器人 `account` 作用域保存敏感配置。

适合存放：

- API key
- token / cookie
- 私有 endpoint
- 不希望写进普通文档或聊天记录的环境变量

常用命令：

```bash
suncodexclawd env set --account <account> --key OPENAI_API_KEY --value 'sk-...'
suncodexclawd env get --account <account> OPENAI_API_KEY
suncodexclawd env list --account <account>
suncodexclawd env run --account <account> --key OPENAI_API_KEY -- your-command
```

说明：

- `env get` / `env list` 默认脱敏显示，不会直接回显明文
- `env run` 在指定 `--account` 时会先注入 `global`，再注入该账号自己的变量；同名时账号作用域优先
- 在机器人工作目录里执行账号作用域命令时，可依赖 `.config.toml` 自动识别 `--account`

## 飞书里的 `/env`

机器人支持直接在飞书里用 `/env` 管理敏感配置，例如：

- `/env help`
- `/env list`
- `/env list global`
- `/env get OPENAI_API_KEY`
- `/env set OPENAI_API_KEY sk-...`
- `/env delete OPENAI_API_KEY`

说明：

- `/env` 默认操作当前机器人账号自己的账号作用域
- `/env list` 默认会同时显示当前账号作用域和 `global` 作用域
- 返回值默认脱敏，不会在聊天里直接泄漏明文

## ClawHub 技能检索

内置 `clawhub` 子命令可直接从 ClawHub 检索公开 skills。

常用命令：

```bash
suncodexclawd clawhub search "timer skill"
suncodexclawd clawhub list --sort updated
suncodexclawd clawhub show openclaw/universal-skills-manager
suncodexclawd clawhub file openclaw/universal-skills-manager --path SKILL.md
```

说明：

- 推荐流程是先 `search` 找候选，再 `show` 看元数据，最后用 `file --path SKILL.md` 读具体技能说明
- 也支持 `--docker-compose`
- 默认访问 `https://clawhub.ai`，可通过 `--base-url` 或 `CLAWHUB_BASE_URL` 覆盖

## 工作区文档刷新

如果你希望忽略远端 WebDAV 当前快照，直接把工作区文档刷新成当前代码内置的最新模板，可以使用：

```bash
suncodexclawd workspace-docs refresh --account <account>
```

如果已经在对应机器人工作目录里，也可以直接：

```bash
suncodexclawd workspace-docs refresh
```

说明：

- 该命令会跳过 restore，直接覆盖 `agent.md`、`soul.md`、`heartbeats.md`
- 同时会刷新 `.config.toml`
- 飞书里也支持 `/docs help`、`/docs refresh`、`/workspace-docs help` 或 `/workspace-docs refresh`

## 文档同步与备份

内置 `sync` 子系统用于备份工作区中的 3 个核心文档：

- `agent.md`
- `soul.md`
- `heartbeats.md`

当前支持：

- `status`
- `list-remote`
- `push`
- `pull`
- `restore`

推荐把 WebDAV 配置写进 `config/secrets/local.toml`：

```toml
[sync.default]
provider = "webdav"

[sync.default.webdav]
url = "https://dav.example.com/remote.php/dav/files/your-user"
username = "your-user"
password = "your-password"
base_path = "/SunCodexClaw/backups"

[sync.assistant]
workspace_id = "assistant"
```

说明：

- `workspace_id` 现在按机器人独立解析
- 如果未显式配置 `[sync.<account>].workspace_id`，默认就使用该机器人的账号名派生值
- 不建议在共享层为多个机器人设置同一个 `workspace_id`

常用命令：

```bash
suncodexclawd sync status --account <account>
suncodexclawd sync list-remote --account <account>
suncodexclawd sync push --account <account>
suncodexclawd sync pull --account <account>
suncodexclawd sync pull --account <account> --to .runtime/sync/restore/<account-namespace>/latest
suncodexclawd sync restore --account <account> --snapshot latest
```

说明：

- `pull` 默认拉到当前机器人自己的 `.runtime/sync/restore/<account-namespace>/<snapshot>` 目录，不直接覆盖工作区
- 这里的 `<account-namespace>` 是账号名按本地目录规则清洗后的结果，例如空格和斜杠会转成 `-`
- `restore` 默认只补缺失文件
- 显式加 `--force` 时才覆盖已有文件

## 飞书里的 `/sync`

机器人支持直接在飞书里用 `/sync` 管理文档备份：

- `/sync help`
- `/sync status`
- `/sync list`
- `/sync push`
- `/sync pull`
- `/sync pull 20260320T010203Z`
- `/sync restore latest`

说明：

- `/sync` 默认操作当前机器人账号的同步配置
- `/sync pull` 默认拉到 `.runtime/sync/restore/<account-namespace>/<snapshot>`

## 更新

更新本机二进制：

```bash
suncodexclawd update
```

只检查最新发行版：

```bash
suncodexclawd update --check
```

更新 Compose 部署：

```bash
suncodexclawd update --docker-compose
suncodexclawd update --docker-compose --project-dir /path/to/SunCodexClaw
```

这会重建并重启容器。

- `--project-dir` 用来指定本地 `docker compose` 项目目录
- Compose 更新不是“下载 GitHub release 二进制替换本机文件”，所以不支持 `--repo/--version/--bin/--check/--dry-run`
- 如果你只是想显式声明“还是本机模式”，也可以写 `suncodexclawd update --local`

## 常见问题

### 机器人已启动，但没有回复

优先看日志里有没有 `FEISHU_EVENT`。

- 没有 `FEISHU_EVENT`
  - 飞书事件没有送到机器人
  - 先检查是否订阅了 `im.message.receive_v1`
- 有 `skip_reason=require_mention_not_met`
  - 群里没有正确 `@` 机器人
- 有 `reply=error mode=codex`
  - 飞书收消息正常，但 Codex/OpenAI 调用或回写失败

### GHCR 镜像名大小写报错

镜像名必须是小写。默认镜像名是：

```text
ghcr.io/rainoffallingstar/suncodexclaw:main
```

### 重新初始化配置

直接重新运行：

```bash
suncodexclawd configure --account <account>
```

或者先备份后重建：

```bash
mv config/secrets/local.toml config/secrets/local.toml.bak
mv config/feishu/bots.toml config/feishu/bots.toml.bak
suncodexclawd configure --account <account>
```

## Docker 镜像

当前 GHCR 镜像会发布 `linux/amd64` 和 `linux/arm64` 多架构镜像。

- 如果你只是想直接运行预构建镜像，可以先 `docker pull`
- 如果你使用仓库自带的 `docker-compose.yml`，`start/restart/update --docker-compose` 默认会先拉取 GHCR 镜像，只有拉取失败时才回退到本地构建

```bash
docker pull ghcr.io/rainoffallingstar/suncodexclaw:main
```

## 兼容脚本

- `tools/install_feishu_launchagents.sh` 仍保留为兼容包装脚本（仅 macOS，本机模式）
