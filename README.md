# SunCodexClaw

把飞书消息 → Codex CLI 执行（读写工作区/跑命令/产出文件）→ 结果/附件/云文档进度回写飞书。

控制面是 Go：`suncodexclawd`（统一 `ctl` 命令）；执行面在容器里跑 Node 机器人脚本（镜像已内置依赖和 `codex` CLI）。

![Quick Start](docs/images/quickstart-terminal.svg)

## 快速开始（Docker Compose，推荐）

前提：

- Docker / Docker Compose
- 一个飞书企业自建应用（启用机器人 + 事件订阅 WebSocket）
- 一套可用的 Codex/OpenAI 凭据（推荐写进 `config/secrets/local.yaml` 的 `codex.api_key`）

### 1) 拉取仓库（拿到 compose 与配置模板）

```bash
git clone https://github.com/rainoffallingstar/SunCodexClaw.git
cd SunCodexClaw
```

### 2) 准备目录与模板

建议把容器内工作区固定为 `/workspace`，后续把每个账号的 `codex.cwd` 写成 `/workspace`（或 `/workspace/<repo>`）。

```bash
mkdir -p .codex config .runtime workspace
cp config/secrets/local.example.yaml config/secrets/local.yaml
cp config/feishu/default.example.json config/feishu/default.json
cp config/feishu/default.example.json config/feishu/assistant.json
```

目录说明：

- `.codex`：可选。若你只用 `codex.api_key`，可以不挂载；若你要在容器里 `codex login`、用 profiles/skills，建议挂载。
- `config`：必挂载。包含 `local.yaml`（敏感项）和每个机器人账号的覆盖配置 `config/feishu/<account>.json`。
- `.runtime`：必挂载。用于日志/pid/自动探测到的 `bot_open_id` 等运行态持久化。
- `workspace`：必挂载。你的真实工作目录（代码库/文件）将映射到容器内 `/workspace`。

### 3) 交互式补齐缺失配置（推荐）

向导会“发现缺失项 → 逐个询问 → 按推荐拆分写入”：

- `config/secrets/local.yaml`：敏感项（飞书密钥、`codex.api_key`、可选 `codex.base_url`）
- `config/feishu/<account>.json`：非敏感运行项（如 `bot_name`、`progress`、`codex.cwd/add_dirs` 等）

```bash
docker run --rm -it \
  --env-file .env \
  -v "$PWD/.codex:/home/node/.codex" \
  -v "$PWD/config:/app/config" \
  -v "$PWD/.runtime:/app/.runtime" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/rainoffallingstar/SunCodexClaw:main \
  configure --account assistant
```

如果你希望“自动生成/无人值守”，可以把关键项放到 `.env`，并使用 `--yes --from-env`：

```bash
cat > .env <<'EOF'
FEISHU_APP_ID=cli_xxx
FEISHU_APP_SECRET=...
FEISHU_ENCRYPT_KEY=...
FEISHU_VERIFICATION_TOKEN=...
FEISHU_CODEX_API_KEY=sk-...
# 可选（自建网关/代理）
# FEISHU_CODEX_BASE_URL=https://api.openai.com/v1
EOF

docker run --rm -it \
  --env-file .env \
  -v "$PWD/config:/app/config" \
  -v "$PWD/.runtime:/app/.runtime" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/rainoffallingstar/SunCodexClaw:main \
  configure --account assistant --yes --from-env
```

说明：

- `.env` 默认只影响 `docker compose` 的变量替换；要传进容器请用 `--env-file`（或自行在 compose 里配置 `environment:`）。
- 若你要多账号分别注入环境变量，可用账号前缀：`FEISHU_<ACCOUNT>_APP_ID`、`FEISHU_<ACCOUNT>_CODEX_API_KEY`（例如 `FEISHU_ASSISTANT_APP_ID`）。

如果你要检查配置是否能跑（不真正启动）：

```bash
docker run --rm \
  -v "$PWD/config:/app/config" \
  -v "$PWD/.runtime:/app/.runtime" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/rainoffallingstar/SunCodexClaw:main \
  preflight assistant
```

### 4) 启动服务

```bash
cp .env.example .env
docker compose up -d
docker compose logs -f
```

### 5) 日常管理（Go ctl）

```bash
docker compose exec suncodexclaw suncodexclawd list
docker compose exec suncodexclaw suncodexclawd status all
docker compose exec suncodexclaw suncodexclawd logs assistant -f
docker compose exec suncodexclaw suncodexclawd restart assistant
docker compose exec suncodexclaw suncodexclawd stop all
```

如果你不使用 compose，也可以直接 `docker run`：

```bash
docker run --rm \
  -v "$PWD/.codex:/home/node/.codex" \
  -v "$PWD/config:/app/config" \
  -v "$PWD/.runtime:/app/.runtime" \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/rainoffallingstar/SunCodexClaw:main start
```

## 配置文件布局（推荐）

![Config Layout](docs/images/config-layout.svg)

### `config/secrets/local.yaml`（敏感项主配置）

这是推荐的主配置位置，尤其是敏感项：

```yaml
config:
  feishu:
    assistant:
      app_id: "cli_xxx"
      app_secret: "..."
      encrypt_key: "..."
      verification_token: "..."
      bot_open_id: "ou_xxx" # 可选：也可在第一次成功 @ 后自动探测并持久化
      bot_name: "飞书 Codex 助手"
      domain: "feishu"
      reply_mode: "codex"
      reply_prefix: "AI 助手："
      require_mention: true
      require_mention_group_only: true
      progress:
        enabled: true
        mode: "doc"
        doc:
          title_prefix: "AI 助手｜任务进度"
          share_to_chat: true
          link_scope: "same_tenant"
          include_user_message: true
          write_final_reply: true
      codex:
        bin: "codex"
        api_key: "sk-..."
        base_url: "https://api.openai.com/v1" # 可选：自建网关/代理可改这里
        model: "gpt-5.4"
        reasoning_effort: "xhigh"
        cwd: "/workspace"
        add_dirs: []
        history_turns: 6
        sandbox: "danger-full-access"
        approval_policy: "never"
```

### `config/feishu/<account>.json`（非敏感覆盖）

这个文件适合放“每台机器不一样”的运行参数：

```json
{
  "bot_name": "AI 助手",
  "reply_mode": "codex",
  "reply_prefix": "AI 助手：",
  "require_mention": true,
  "require_mention_group_only": true,
  "progress": {
    "enabled": true,
    "mode": "doc",
    "doc": { "title_prefix": "AI 助手｜任务进度" }
  },
  "codex": {
    "cwd": "/workspace",
    "add_dirs": []
  }
}
```

配置建议：

- 密钥尽量只放 `local.yaml`
- `config/feishu/<account>.json` 留给运行参数覆盖
- 部署时优先显式填写 `bot_name`（群里 @ 识别更稳定）
- 每个机器人单独配置 `codex.cwd`
- 如果需要跨多个目录工作，可以额外配置 `codex.add_dirs`
- `bot_open_id` 可以不手填；群里第一次成功 @ 机器人后会自动探测并持久化
- 如果你已经通过 `codex login` 登录，也可以不填 `codex.api_key`

## 常用部署细节

### 固定工作目录（容器内）

推荐约定：

- 把宿主机工作区挂载到 `/workspace`
- 把每个账号的 `codex.cwd` 写成 `/workspace`（或 `/workspace/<repo>`）
- 需要跨目录再配置 `codex.add_dirs`

### Codex Base URL（自建网关/代理）

优先级（从高到低）：

1. CLI 参数：`--codex-base-url`
2. 环境变量：`FEISHU_CODEX_BASE_URL` / `OPENAI_BASE_URL` / `OPENAI_API_BASE`
3. 配置：`codex.base_url`

### 容器权限（避免 root/写权限问题）

容器需要写入挂载目录以持久化 `.runtime/` 与（可选）配置落盘。

`docker-compose.yml` 默认使用：

```text
user: "${SUNCODEXCLAW_UID:-1000}:${SUNCODEXCLAW_GID:-1000}"
```

在 Linux 上如遇到权限问题，通常把 `.env` 里设置为当前用户即可：

```bash
echo "SUNCODEXCLAW_UID=$(id -u)" >> .env
echo "SUNCODEXCLAW_GID=$(id -g)" >> .env
docker compose up -d
```

## Docker 镜像

镜像由 GitHub Actions 自动构建并推送到 GHCR：

```bash
docker pull ghcr.io/rainoffallingstar/SunCodexClaw:main
```

## Deprecated

- `tools/install_feishu_launchagents.sh`：兼容 wrapper（已 deprecated），请改用 `suncodexclawd launchagents ...`
- `tools/configure_docker_config.js`：旧的 Docker 配置向导（已 deprecated），请改用 `suncodexclawd configure`
- `tools/feishu_bot_ctl.sh`：兼容 ctl 脚本（建议直接用 `suncodexclawd <cmd>`）
