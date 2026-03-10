# SunCodexClaw

`SunCodexClaw` 是一个可独立运行的飞书机器人项目。它通过飞书 WebSocket 长连接接收消息，把请求交给本机 `codex exec` 处理，并把过程回写到飞书消息或飞书云文档。

这套项目已经内置了几项实战能力：

- 文本消息自动回复
- 群聊 `@机器人` 触发
- 图片消息下载后交给 Codex 分析
- 文件消息下载到本地临时路径后交给 Codex 读取
- Codex 反向请求“把本机某个文件发回飞书”
- 多线程会话上下文
- 进度实时回写到飞书消息或云文档
- 多账号、多工作目录、多机器人统一启停

## 目录结构

```text
SunCodexClaw/
├── config/
│   ├── feishu/
│   │   ├── default.example.json
│   │   └── <account>.json
│   └── secrets/
│       ├── local.example.yaml
│       └── local.yaml
├── tools/
│   ├── feishu_ws_bot.js
│   ├── feishu_bot_ctl.sh
│   ├── install_feishu_launchagents.sh
│   ├── save_feishu_account_keychain.sh
│   └── lib/local_secret_store.js
└── README.md
```

## 运行要求

- macOS 或 Linux
- Node.js 18+
- 已安装并能在终端执行的 `codex`
- 一个飞书自建应用
- 应用已启用机器人能力
- 应用已发布到当前租户并可被会话拉入

首次安装依赖：

```bash
cd /Users/Sun/Code/SunCodexClaw
npm install
```

## 配置模型

项目把配置拆成两层：

- `config/feishu/<account>.json`
  - 放每个机器人的非敏感运行配置
  - 例如：工作目录、回复模式、云文档模式、会话规则、回复前缀
- `config/secrets/local.yaml`
  - 放敏感信息和本机私有配置
  - 例如：OpenAI API Key、飞书 App ID、App Secret、Encrypt Key、Verification Token、Bot Open ID

另外也支持 macOS Keychain。当前读取顺序是：

1. 命令行参数
2. 环境变量
3. `config/secrets/local.yaml`
4. `config/feishu/<account>.json`
5. macOS Keychain

敏感文件默认不会进入 Git：

- `config/feishu/*.json`
- `config/secrets/*.yaml`
- `.runtime/`

## 基本配置

先复制模板：

```bash
cp config/feishu/default.example.json config/feishu/default.json
cp config/secrets/local.example.yaml config/secrets/local.yaml
```

然后按账号创建独立配置，例如：

```bash
cp config/feishu/default.json config/feishu/assistant.json
```

### `config/feishu/<account>.json`

这一层推荐只保留运行项，比如：

```json
{
  "domain": "feishu",
  "reply_mode": "codex",
  "reply_prefix": "AI 助手：",
  "ignore_self_messages": true,
  "auto_reply": true,
  "require_mention": true,
  "require_mention_group_only": true,
  "progress": {
    "enabled": true,
    "message": "已接收，正在执行。",
    "mode": "doc",
    "doc": {
      "title_prefix": "AI 助手｜任务进度",
      "share_to_chat": true,
      "link_scope": "same_tenant",
      "include_user_message": true,
      "write_final_reply": true
    }
  },
  "codex": {
    "bin": "codex",
    "model": "gpt-5.4",
    "reasoning_effort": "xhigh",
    "cwd": "/absolute/path/to/workspace",
    "history_turns": 6,
    "system_prompt": "你是“飞书 Codex 助手”，通过飞书和用户交流。请直接回答用户问题，不要复述用户原话。",
    "keychain": {
      "api_key_service": "openai-api-key",
      "api_key_fallback_service": "openai-api-key"
    }
  },
  "keychain": {
    "app_id_service": "feishu-app-id:assistant",
    "app_secret_service": "feishu-app-secret:assistant",
    "encrypt_key_service": "feishu-encrypt-key:assistant",
    "verification_token_service": "feishu-verification-token:assistant",
    "bot_open_id_service": "feishu-bot-open-id:assistant"
  }
}
```

### `config/secrets/local.yaml`

这一层放敏感值，例如：

```yaml
services:
  "openai-api-key": "sk-..."
  "feishu-app-id:assistant": "cli_xxx"
  "feishu-app-secret:assistant": "..."
  "feishu-encrypt-key:assistant": "..."
  "feishu-verification-token:assistant": "..."
  "feishu-bot-open-id:assistant": "ou_xxx"

config:
  feishu:
    assistant:
      codex:
        cwd: "/absolute/path/to/workspace"
```

## Keychain 可选方案

如果你不想把飞书凭据写进 `local.yaml`，可以写到 macOS Keychain：

```bash
bash tools/save_feishu_account_keychain.sh \
  assistant \
  <app_id> \
  <app_secret> \
  <encrypt_key> \
  <verification_token> \
  <bot_open_id>
```

默认 Keychain account 名称是 `codex-claw`。如需改名，启动前设置：

```bash
export SUNCODEXCLAW_KEYCHAIN_ACCOUNT="your-keychain-account"
```

## 飞书开放平台配置

### 1. 创建应用

在飞书开放平台创建一个企业自建应用，启用：

- 机器人能力
- 事件订阅能力
- 云文档 / 云盘能力

### 2. 填写应用安全信息

你需要拿到并放进配置里的值有：

- `App ID`
- `App Secret`
- `Encrypt Key`
- `Verification Token`
- `Bot Open ID`

其中前四项在开放平台应用设置里可见，`Bot Open ID` 可以在机器人实际收到消息后的事件体里获取，也可以通过开放平台调试工具拿到。

### 3. 事件订阅

当前机器人必须订阅：

- `im.message.receive_v1`

这是 WebSocket 模式的入口事件。没有它，机器人不会收到任何消息。

### 4. 权限

#### 最低必需权限

如果你只用当前这套功能，至少要保证这些能力通：

- `im:message`
- `im:message:readonly`
- `im:message.group_msg`
- `im:message.p2p_msg:readonly`
- `im:message:send_as_bot`
- `im:chat:read`
- `im:chat:readonly`
- `im:chat.members:read`
- `im:resource`
- `docx:document`
- `docx:document:create`
- `docx:document:readonly`
- `docx:document:write_only`
- `drive:drive`
- `drive:drive.metadata:readonly`
- `drive:drive:readonly`

#### 当前项目实测使用的完整权限清单

下面这份是当前项目侧已经放通、并且与现有功能兼容的完整 scopes 示例：

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

### 5. 发布应用

完成权限和事件订阅后，需要：

1. 发布应用版本
2. 安装到目标租户
3. 把机器人拉进对应群聊，或给它建立 P2P 会话

## 启动与管理

### 单机器人调试

只做参数和凭据检查，不建连：

```bash
node tools/feishu_ws_bot.js --account assistant --dry-run
```

前台启动：

```bash
node tools/feishu_ws_bot.js --account assistant
```

### 多机器人统一管理

列出可启动账号：

```bash
bash tools/feishu_bot_ctl.sh list
```

启动全部：

```bash
bash tools/feishu_bot_ctl.sh start all
```

查看状态：

```bash
bash tools/feishu_bot_ctl.sh status all
```

查看日志：

```bash
bash tools/feishu_bot_ctl.sh logs assistant --follow
```

重启单个账号：

```bash
bash tools/feishu_bot_ctl.sh restart assistant
```

### 开机自启

在 macOS 上可安装 LaunchAgents：

```bash
bash tools/install_feishu_launchagents.sh install all
```

查看 LaunchAgents 状态：

```bash
bash tools/install_feishu_launchagents.sh status all
```

默认 launchctl label 前缀是 `com.sunbelife.suncodexclaw.feishu`。如需自定义：

```bash
export SUNCODEXCLAW_LAUNCHCTL_PREFIX="com.example.suncodexclaw.feishu"
```

## 消息能力说明

### 文本

机器人会把消息交给本机 `codex exec --json` 处理，并保留多轮上下文。

支持线程命令：

- `/threads` 或 `/thread list`
- `/thread new [名称]`
- `/thread switch <线程ID或名称>`
- `/thread current`
- `/reset`

### 图片

图片会先下载到本地，再作为输入传给 Codex。

### 文件读取

用户直接发送飞书文件消息时：

1. 机器人会把文件下载到本地临时目录
2. 把真实临时路径写进提示词
3. Codex 可以直接读取这个文件
4. 回复完成后临时目录会清理

### 文件发送

Codex 如果要把本机文件发回飞书，可以在最终输出里单独占行写：

```text
[[FEISHU_SEND_FILE:/absolute/or/relative/path]]
```

支持多行多个文件。机器人会：

1. 解析这些隐藏指令
2. 上传对应本地文件到飞书
3. 以文件消息形式发回会话

当前单文件上限为 `30 MB`。

### 群聊触发规则

默认规则是：

- P2P 会话可直接说话
- 群聊需要 `@机器人`

并且已经支持一个实用补偿：

- 如果同一个人在群里先 `@机器人`
- 然后 2 分钟内单独发文件 / 图片 / 富文本
- 机器人会把后续消息继续当作同一轮任务处理

## 进度模式

支持两种进度输出：

- `message`
  - 进度写在飞书消息里
- `doc`
  - 创建飞书云文档
  - 任务过程持续写入文档
  - 会话里只发文档链接和最终结果

云文档模式会写入：

- 任务概览
- 用户消息
- 进度日志
- 执行命令
- `stdout` / `stderr`
- 最终回复

命令和输出会以代码块形式写进文档。

## 常用问题

### 机器人不回消息

先检查：

- 应用是否已发布
- 群里是否真的 `@` 到了这个 bot
- 是否订阅了 `im.message.receive_v1`
- `--dry-run` 是否能读到凭据
- `logs <account> --follow` 是否收到事件

### 云文档创建成功但没有链接

通常是 `docx` 权限开了，但 `drive` 权限没开全。至少要补：

- `drive:drive`
- `drive:drive.metadata:readonly`

### 文件无法发回飞书

检查：

- 文件是否真实存在
- 是否是普通文件而不是目录
- 是否超过 `30 MB`
- 机器人账号是否有 `im:message:send_as_bot` / `im:resource`

## 安全建议

- 不要把 `config/secrets/local.yaml` 提交到 Git
- 不要把真实 `config/feishu/*.json` 提交到公开仓库
- 生产环境尽量通过 Keychain 或 CI Secret 注入敏感值
- 给每个机器人单独的 `codex.cwd`
- 如果机器人需要更广的本机文件访问，请明确评估 `codex` 的权限边界
