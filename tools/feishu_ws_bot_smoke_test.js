const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

test('feishu ws bot can initialize runtime workspace with local toml secrets', () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'suncodexclaw-js-smoke-'));
  const stubRoot = path.join(tmp, 'stub');
  const configRoot = path.join(tmp, 'config');
  const workspaceRoot = path.join(tmp, 'workspace');
  const workspaceDir = path.join(workspaceRoot, 'openclaw');
  const daemonBin = path.join(tmp, 'suncodexclawd');
  const repoRoot = path.resolve(__dirname, '..');
  const scriptPath = path.join(repoRoot, 'tools', 'feishu_ws_bot.js');
  const overlayPath = path.join(configRoot, 'feishu', 'bots.toml');
  const secretsPath = path.join(configRoot, 'secrets', 'local.toml');
  const timerPath = path.join(repoRoot, 'config', 'timers', 'openclaw', 'workspace-doc-sync.json');

  fs.mkdirSync(path.join(stubRoot, 'node_modules', '@larksuiteoapi', 'node-sdk'), { recursive: true });
  fs.writeFileSync(path.join(stubRoot, 'node_modules', '@larksuiteoapi', 'node-sdk', 'index.js'), [
    'class Client { constructor(config) { this.config = config; } }',
    'class EventDispatcher { constructor(config) { this.config = config; } register() { return this; } }',
    'class WSClient { constructor(config) { this.config = config; } async start() { return; } close() {} }',
    'module.exports = {',
    '  Client,',
    '  EventDispatcher,',
    '  WSClient,',
    '  Domain: { Feishu: "feishu", Lark: "lark" },',
    '  LoggerLevel: { info: "info" },',
    '};',
    '',
  ].join('\n'), 'utf8');
  fs.writeFileSync(daemonBin, '#!/bin/sh\nexit 1\n', { mode: 0o755 });

  fs.mkdirSync(path.dirname(overlayPath), { recursive: true });
  fs.mkdirSync(path.dirname(secretsPath), { recursive: true });
  fs.mkdirSync(workspaceRoot, { recursive: true });

  fs.writeFileSync(overlayPath, [
    '[shared]',
    'reply_mode = "echo"',
    'auto_reply = true',
    'ignore_self_messages = true',
    '[shared.progress]',
    'enabled = true',
    'mode = "doc"',
    '[shared.progress.doc]',
    'title_prefix = "AI 助手｜任务进度"',
    'share_to_chat = true',
    'link_scope = "same_tenant"',
    'include_user_message = true',
    'write_final_reply = true',
    '[shared.codex]',
    `cwd_root = ${JSON.stringify(workspaceRoot)}`,
    '[bot.openclaw]',
    'bot_name = "OpenClaw"',
    '',
  ].join('\n'), 'utf8');

  fs.writeFileSync(secretsPath, [
    '[feishu.openclaw]',
    'app_id = "cli_xxx"',
    'app_secret = "secret_xxx"',
    '',
  ].join('\n'), 'utf8');

  try {
    const run = spawnSync(process.execPath, [scriptPath, '--config-dir', configRoot, '--account', 'openclaw'], {
      cwd: repoRoot,
      encoding: 'utf8',
      env: {
        ...process.env,
        NODE_PATH: path.join(stubRoot, 'node_modules'),
        SUNCODEXCLAW_LOCAL_TOML: secretsPath,
        SUNCODEXCLAWD_BIN: daemonBin,
      },
    });

    assert.equal(run.status, 0, run.stderr || run.stdout);
    assert.match(run.stdout, /FEISHU_WS_BOT_RUNNING/);
    assert.match(run.stdout, /runtime_config_toml=/);
    assert.ok(fs.existsSync(path.join(workspaceDir, '.config.toml')));
    assert.ok(fs.existsSync(path.join(workspaceDir, 'agent.md')));
  } finally {
    fs.rmSync(timerPath, { force: true });
    try {
      fs.rmdirSync(path.dirname(timerPath));
    } catch (_) {
      // ignore
    }
    try {
      fs.rmdirSync(path.join(repoRoot, 'config', 'timers'));
    } catch (_) {
      // ignore
    }
  }
});
