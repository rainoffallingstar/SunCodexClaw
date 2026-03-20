# SunCodexClaw

把飞书消息接到容器内，调用 Codex CLI 执行任务，再把结果、附件和进度回写飞书。

推荐用 Docker Compose 部署。仓库内已经包含镜像、配置向导和常用管理命令。

## 你需要准备

- Docker / Docker Compose
- 一个飞书企业自建应用
- 可用的 Codex / OpenAI 凭据

飞书应用至少要满足这些条件：

- 启用机器人能力
- 事件与回调使用“长连接”
- 订阅事件 `im.message.receive_v1`
- 每次修改订阅配置后发布应用版本

## 飞书应用权限

当前项目实测可用的一套完整权限清单如下。

- 这是“当前实测可用的完整权限集合”，不是严格收敛后的最小权限集合
- 如果你是新部署，建议先按这份权限导入，跑通后再按需收缩

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

## 快速开始

### 1. 拉取仓库

```bash
git clone https://github.com/rainoffallingstar/SunCodexClaw.git
cd SunCodexClaw
```

### 2. 准备目录和环境文件

```bash
mkdir -p .codex config/feishu config/secrets .runtime workspace
cp .env.example .env
cp app.env.example app.env
```

说明：

- `.env` 给 `docker compose` 自己做变量替换
- `app.env` 注入容器内，供配置向导和机器人进程读取
- `workspace` 会挂载到容器内的 `/workspace`

### 3. 填写环境变量

最少需要在 `app.env` 里填写：

```dotenv
FEISHU_APP_ID=cli_xxx
FEISHU_APP_SECRET=xxx
FEISHU_ENCRYPT_KEY=xxx
FEISHU_VERIFICATION_TOKEN=xxx
FEISHU_BOT_NAME=openclaw
FEISHU_CODEX_API_KEY=sk-xxx
FEISHU_CODEX_CWD=/workspace
```

常见可选项：

```dotenv
FEISHU_CODEX_BASE_URL=https://api.openai.com/v1
FEISHU_CODEX_MODEL=gpt-5.2
FEISHU_CODEX_REASONING_EFFORT=high
FEISHU_PROGRESS_MODE=doc
FEISHU_PROGRESS_DOC_TITLE_PREFIX=AI 助手｜任务进度
```

如果宿主机和容器写文件权限不一致，可以在 `.env` 里设置：

```dotenv
SUNCODEXCLAW_UID=1000
SUNCODEXCLAW_GID=1000
WORKSPACE_PATH=./workspace
HEALTH_PORT=8080
```

### 4. 生成配置文件

```bash
docker compose run --rm suncodexclaw \
  configure --account assistant --yes --from-env
```

这一步会生成或更新：

- `config/secrets/local.yaml`
- `config/feishu/assistant.json`

### 5. 预检查

```bash
docker compose run --rm suncodexclaw preflight assistant
```

### 6. 启动服务

```bash
docker compose up -d
docker compose logs -f
```

## 验证是否正常

启动成功后，日志里应该能看到：

- `FEISHU_WS_BOT_RUNNING`
- `ws client ready`

然后做两次测试：

1. 私聊机器人发一条消息
2. 群里 `@机器人` 再发一条消息

你当前默认配置下：

- 私聊不需要 `@`
- 群聊需要 `@`

## 常用命令

```bash
docker compose exec suncodexclaw suncodexclawd list
docker compose exec suncodexclaw suncodexclawd status all
docker compose exec suncodexclaw suncodexclawd logs assistant -f
docker compose exec suncodexclaw suncodexclawd restart assistant
docker compose exec suncodexclaw suncodexclawd stop all
docker compose exec suncodexclaw suncodexclawd update --check
```

自更新命令：

```bash
suncodexclawd update --check
suncodexclawd update
```

说明：

- `update --check` 只查看将要下载的 release 资产，不替换本地二进制
- `update` 会按当前机器的 `GOOS/GOARCH` 从 GitHub Release 下载对应包，并替换当前 `suncodexclawd` 二进制
- 替换完成后，正在运行的旧进程不会自动热切换；需要重启后才会运行新版本

## 更新

### 更新容器内 `suncodexclawd` 二进制

如果你当前是源码目录 + Compose 挂载配置运行，并且想只更新守护二进制，可以在容器里执行：

```bash
docker compose exec suncodexclaw suncodexclawd update --check
docker compose exec suncodexclaw suncodexclawd update
docker compose restart suncodexclaw
```

说明：

- `update` 会替换容器内当前 `suncodexclawd` 二进制
- 替换完成后必须重启容器，新的守护进程才会生效

### 更新 Docker Compose 镜像

如果你希望升级整个镜像，而不是只替换容器内二进制，推荐这样做：

```bash
docker compose pull
docker compose up -d --force-recreate
docker compose logs -f
```

如果你固定了标签，也可以先修改 `.env` 里的 `IMAGE_TAG`，再执行：

```bash
docker compose pull
docker compose up -d --force-recreate
```

## 定时任务

当前版本内置了 `suncodexclawd timer` 子系统。

- 容器执行 `start` 时，会默认顺带启动 timer scheduler
- 定时任务配置保存在 `config/timers/*.json`
- 运行状态保存在 `.runtime/timers/state/*.json`
- 定时任务日志保存在 `.runtime/timers/logs/*.log`

常用命令：

```bash
docker compose exec suncodexclaw suncodexclawd timer list
docker compose exec suncodexclaw suncodexclawd timer show daily-report
docker compose exec suncodexclaw suncodexclawd timer run daily-report
docker compose exec suncodexclaw suncodexclawd timer logs daily-report
docker compose exec suncodexclaw suncodexclawd timer disable daily-report
docker compose exec suncodexclaw suncodexclawd timer enable daily-report
docker compose exec suncodexclaw suncodexclawd timer delete daily-report
```

创建或更新一个任务：

```bash
docker compose exec suncodexclaw suncodexclawd timer upsert \
  --id daily-report \
  --account assistant \
  --chat-id oc_xxx \
  --daily 09:00 \
  --tz Asia/Shanghai \
  --cwd /workspace \
  --prompt "检查 /workspace 仓库并输出日报"
```

支持的调度方式：

- `--every 1h`
- `--daily 09:00`
- `--weekly mon,tue,fri --at 09:00`

## 飞书里的 `/timer`

机器人默认会被告知如何使用 `suncodexclawd timer`。

你可以直接在飞书里发送 `/timer ...` 让机器人帮你管理定时任务，例如：

- `/timer list`
- `/timer show daily-report`
- `/timer run daily-report`
- `/timer logs daily-report`
- `/timer enable daily-report`
- `/timer disable daily-report`
- `/timer delete daily-report`
- `/timer 创建一个每天 09:00 执行的日报任务，目录 /workspace，结果发回当前会话`
- `/timer 删除 daily-report`

其中 `list/show/run/logs/enable/disable/delete` 这类格式化命令会直接调用本地 `suncodexclawd timer ...`。

更复杂的自然语言 `/timer ...` 请求会交给机器人翻译成对应的 `suncodexclawd timer ...` 命令来执行。

## 配置文件说明

推荐把配置拆成两层：

- `config/secrets/local.yaml`
  - 放敏感项，如飞书密钥、`codex.api_key`
- `config/feishu/<account>.json`
  - 放运行项，如 `bot_name`、`progress`、`codex.cwd`

常用字段：

- `bot_name`
- `require_mention`
- `require_mention_group_only`
- `codex.cwd`
- `codex.add_dirs`
- `codex.model`
- `codex.reasoning_effort`
- `progress.mode`

## 常见问题

### 机器人已启动，但没有任何回复

先看日志里有没有 `FEISHU_EVENT`：

- 没有 `FEISHU_EVENT`
  - 飞书没有把消息事件发过来
  - 优先检查是否订阅了 `im.message.receive_v1`
- 有 `FEISHU_EVENT`，但出现 `skip_reason=require_mention_not_met`
  - 群里没有正确 `@` 机器人
- 有 `reply=error mode=codex`
  - 飞书收消息正常，但 Codex/OpenAI 调用或回写失败

### `configure --from-env` 之后配置还是示例值

`configure --from-env` 只会填“缺失项”，不会覆盖已有值。

如果你之前把示例文件直接复制成了正式配置文件，请先删掉或手工改掉占位值，再重新执行：

```bash
mv config/secrets/local.yaml config/secrets/local.yaml.bak
docker compose run --rm suncodexclaw \
  configure --account assistant --yes --from-env
```

### GHCR 镜像名大小写报错

镜像名必须是小写。默认镜像名是：

```text
ghcr.io/rainoffallingstar/suncodexclaw:main
```

## Docker 镜像

```bash
docker pull ghcr.io/rainoffallingstar/suncodexclaw:main
```

## 兼容脚本

这些脚本仍然保留，但新部署不推荐再用：

- `tools/install_feishu_launchagents.sh`
- `tools/configure_docker_config.js`
- `tools/feishu_bot_ctl.sh`
