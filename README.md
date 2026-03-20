# SunCodexClaw

把飞书消息接到本地或容器中的 Codex CLI，执行任务后再把结果、进度和附件回写飞书。

当前推荐的部署模型很简单：

- 业务配置只认两个 TOML 文件
- 默认本机直接运行
- 只有显式加 `--docker-compose` 时才走 Docker Compose
- `start/restart/status/stop/preflight` 不带 `--account` 时默认处理所有 `enabled = true` 的机器人
- `list/configure/timer/memory/sync` 也支持 `--docker-compose`
- `list` 会列出所有已配置机器人及其 `enabled/disabled` 状态
- `timer/memory/sync` 这类账号作用域命令在仓库根目录下建议显式带 `--account`，在机器人工作目录中可依赖 `.config.toml` 自动判定

## 你需要准备

- Node.js 20+
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
  - 每个机器人可单独配置 `enabled = true|false`，默认 `true`
- `config/secrets/local.toml`
  - 飞书凭据、Codex/OpenAI 密钥、WebDAV 等敏感配置

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
- 如果只配置了 `shared.codex.cwd_root = "workspace"`，每个机器人会自动派生自己的工作目录 `workspace/<account>`
- 这样同一份配置可以同时兼容本机模式和 Docker Compose 模式

### 3. 初始化配置

```bash
suncodexclawd configure --account assistant
```

追加第二个机器人：

```bash
suncodexclawd configure add --account reviewer
```

`configure` 的行为：

- 直接对 `config/feishu/bots.toml` 与 `config/secrets/local.toml` 做交互式 add/edit
- 已有值会作为默认值回显，直接回车即可保留
- 不再生成容器内配置文件
- 不再从环境变量导入业务配置
- 如果你更习惯宿主机只保留 Compose，也可以执行 `suncodexclawd configure --docker-compose --account assistant`

### 4. 预检查

本机模式：

```bash
suncodexclawd preflight --account assistant
```

容器模式：

```bash
suncodexclawd preflight --docker-compose --account assistant
```

### 5. 启动

默认本机直接运行：

```bash
suncodexclawd start
suncodexclawd logs --account assistant -f
```

如果你要用 Docker Compose，显式加 `--docker-compose`：

```bash
suncodexclawd start --docker-compose
```

说明：

- `start/restart/status/stop/logs/preflight` 默认都按本机模式执行
- `start/restart/status/stop/preflight` 不带 `--account` 时，会处理 `bots.toml` 中所有 `enabled = true` 的机器人
- 如果某个机器人暂时不想启动，直接在 `[bot.<account>]` 下设置 `enabled = false`
- `list/configure/timer/memory/sync` 也支持显式 `--docker-compose`
- 这些工具型命令在 Compose 模式下会优先执行 `docker compose exec suncodexclaw suncodexclawd <subcommand> ...`
- 如果 Compose 服务还没启动，再回退到 `docker compose run --rm suncodexclaw <subcommand> ...`
- `start --docker-compose` 会执行 `docker compose up -d --build`
- `restart --docker-compose` 会执行 `docker compose up -d --build --force-recreate`
- `update --docker-compose` 会重建并重启容器服务
- 如果本机没有 `docker`，显式使用 `--docker-compose` 会直接报错退出

## 运行模式

### 本机模式

- 默认模式
- 适合开发、调试、或不想依赖 Docker 的部署
- 由 `suncodexclawd` 直接拉起本机进程

### Docker Compose 模式

- 仅在显式使用 `--docker-compose` 时启用
- Compose 会挂载 `config/`、`.runtime/`、`.codex/` 和 `workspace/`
- 容器内执行 `suncodexclawd start` 时仍然按默认本机模式启动，不会再套一层 Docker
- 默认会把宿主机的 `WORKSPACE_PATH` 挂到容器内 `/app/workspace`，因此推荐把 `shared.codex.cwd_root` 配成相对路径 `workspace`

`.env.example` 只包含 Compose 相关变量，例如：

```dotenv
GITHUB_REPOSITORY=rainoffallingstar/suncodexclaw
IMAGE_TAG=main
WORKSPACE_PATH=./workspace
HEALTH_PORT=8080
SUNCODEXCLAW_UID=1000
SUNCODEXCLAW_GID=1000
```

## 多机器人

多机器人不需要额外开关，仓库会自动按配置感知。

规则：

- `config/feishu/bots.toml` 中每个 `[bot.<account>]` 代表一个机器人
- 账号作用域命令始终显式使用 `--account <account>`
- 这条规则在单机器人场景下也成立

推荐习惯：

- `suncodexclawd start --account assistant`
- `suncodexclawd timer list --account assistant`
- `suncodexclawd memory search 中文 --account assistant`
- `suncodexclawd sync push --account assistant`

## 工作区初始化

每个机器人第一次进入自己的运行目录时，会自动完成这些动作：

- 初始化该运行目录为 Git 仓库
- 写入 `.config.toml`
- 检查 `agent.md`、`soul.md`、`heartbeats.md`
- 如果已配置 WebDAV，同步系统会先尝试 `sync restore`
- restore 没补齐的文件，才会按默认模板创建
- 自动创建当前机器人的默认文档同步任务 `workspace-doc-sync`

`.config.toml` 会记录：

- 当前机器人账号
- 当前工作目录
- 相关配置文件路径
- 当前目录对应的 `timer/memory/sync` 账号作用域

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
- `agent.md` 会说明 `timer/memory/sync` 在本目录可直接依赖 `.config.toml` 识别账号
- `agent.md` 会说明 `list/configure/timer/memory/sync` 支持 `--docker-compose`
- `heartbeats.md` 会提醒记录新安装的技能

## 常用命令

本机模式：

```bash
suncodexclawd list
suncodexclawd status --account assistant
suncodexclawd logs --account assistant -f
suncodexclawd restart --account assistant
suncodexclawd memory search 中文 --account assistant
suncodexclawd sync status --account assistant
suncodexclawd update --check
```

如果服务已经跑在 Compose 里，也可以直接进容器执行：

```bash
docker compose exec suncodexclaw suncodexclawd status --account assistant
docker compose exec suncodexclaw suncodexclawd logs --account assistant -f
docker compose exec suncodexclaw suncodexclawd memory list --account assistant
```

如果你希望统一从宿主机通过 Compose 调工具命令，也可以直接这样写：

```bash
suncodexclawd list --docker-compose
suncodexclawd configure --docker-compose --account assistant
suncodexclawd timer list --docker-compose --account assistant
suncodexclawd memory search 中文 --docker-compose --account assistant
suncodexclawd sync status --docker-compose --account assistant
```

如果你已经位于某个机器人自己的工作目录里，也可以直接依赖 `.config.toml`：

```bash
suncodexclawd memory list
suncodexclawd timer list
suncodexclawd sync status
```

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
  --account assistant \
  --chat-id oc_xxx \
  --daily 09:00 \
  --tz Asia/Shanghai \
  --cwd workspace/assistant \
  --prompt "检查仓库并把日报发回当前会话"
```

更新已有任务：

```bash
suncodexclawd timer update daily-report \
  --account assistant \
  --daily 10:30 \
  --prompt "检查仓库并发送更新后的日报"
```

每个机器人第一次启动进入自己的工作目录时，会自动创建一个文档同步任务：

- 任务名：`workspace-doc-sync`
- 动作：同步 `agent.md`、`soul.md`、`heartbeats.md`
- 周期：每 24 小时一次
- 存储位置：`config/timers/<account>/workspace-doc-sync.json`
- 如果尚未配置 WebDAV，任务会以 `--skip-if-unconfigured` 安静跳过

## 飞书里的 `/timer`

机器人支持直接在飞书里用 `/timer` 管理任务，例如：

- `/timer list`
- `/timer show daily-report`
- `/timer run daily-report`
- `/timer logs daily-report`
- `/timer enable daily-report`
- `/timer disable daily-report`
- `/timer delete daily-report`
- `/timer 创建一个每天 09:00 执行的日报任务，目录 workspace/assistant，结果发回当前会话`
- `/timer 把 daily-report 改成每天 10:30 执行，并把任务内容改成检查当前工作目录后发回当前会话`

## 记忆系统

内置 `memory` 子系统会按机器人账号分库。

- 每个机器人使用独立记忆库
- 存储位置：`config/memory/libraries/<account>/entries/*.json`
- 适合存长期偏好、长期规则、检索线索

常用命令：

```bash
suncodexclawd memory add --account assistant --text "以后默认用简体中文回复"
suncodexclawd memory list --account assistant
suncodexclawd memory search 中文 --account assistant
suncodexclawd memory show mem-20260320-090000-000 --account assistant
suncodexclawd memory delete mem-20260320-090000-000 --account assistant
```

## 飞书里的 `/memory`

机器人支持直接在飞书里用 `/memory` 管理长期记忆，例如：

- `/memory 以后默认用简体中文回复`
- `/memory add 代码修改后默认顺手跑测试`
- `/memory list`
- `/memory search 中文`
- `/memory show mem-20260320-090000-000`
- `/memory delete mem-20260320-090000-000`

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
- 如果未显式配置 `[sync.<account>].workspace_id`，默认就使用该机器人的账号名
- 不建议在共享层为多个机器人设置同一个 `workspace_id`

常用命令：

```bash
suncodexclawd sync status --account assistant
suncodexclawd sync list-remote --account assistant
suncodexclawd sync push --account assistant
suncodexclawd sync pull --account assistant --to .runtime/sync/restore/latest
suncodexclawd sync restore --account assistant --snapshot latest
```

说明：

- `pull` 只拉到本地 staging/restore 目录，不直接覆盖工作区
- `restore` 默认只补缺失文件
- 显式加 `--force` 时才覆盖已有文件

## 飞书里的 `/sync`

机器人支持直接在飞书里用 `/sync` 管理文档备份：

- `/sync status`
- `/sync list`
- `/sync push`
- `/sync pull`
- `/sync pull 20260320T010203Z`
- `/sync restore latest`

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
```

这会重建并重启容器。下载或重建完成后，命令会提示你重启服务。

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
suncodexclawd configure --account assistant
```

或者先备份后重建：

```bash
mv config/secrets/local.toml config/secrets/local.toml.bak
mv config/feishu/bots.toml config/feishu/bots.toml.bak
suncodexclawd configure --account assistant
```

## Docker 镜像

```bash
docker pull ghcr.io/rainoffallingstar/suncodexclaw:main
```

## 兼容脚本

这些脚本仍然保留，但不推荐新部署继续依赖：

- `tools/feishu_bot_ctl.sh`
- `tools/install_feishu_launchagents.sh`
