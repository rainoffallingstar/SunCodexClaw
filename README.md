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
```

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
