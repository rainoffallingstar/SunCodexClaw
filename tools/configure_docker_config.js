#!/usr/bin/env node
/* eslint-disable no-console */
// Deprecated: prefer Go config wizard.
//   npm run go:configure
const fs = require('fs');
const path = require('path');
const readline = require('readline');
let YAML = null;
try {
  // eslint-disable-next-line global-require
  YAML = require('yaml');
} catch (_) {
  YAML = null;
}

function repoRoot() {
  return path.resolve(__dirname, '..');
}

function ensureDir(p) {
  fs.mkdirSync(p, { recursive: true });
}

function readJson(filePath) {
  return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}

function writeJson(filePath, obj) {
  const out = `${JSON.stringify(obj, null, 2)}\n`;
  fs.writeFileSync(filePath, out, 'utf8');
}

function readYamlIfExists(filePath) {
  if (!YAML) return null;
  if (!fs.existsSync(filePath)) return null;
  return YAML.parse(fs.readFileSync(filePath, 'utf8'));
}

function writeYaml(filePath, obj) {
  if (!YAML) throw new Error('missing dependency "yaml"; run: npm install');
  const doc = new YAML.Document(obj);
  doc.contents = obj;
  fs.writeFileSync(filePath, String(doc), 'utf8');
}

function copyIfMissing(src, dst) {
  if (fs.existsSync(dst)) return false;
  ensureDir(path.dirname(dst));
  fs.copyFileSync(src, dst);
  return true;
}

function ask(rl, question, def = '') {
  const suffix = def ? ` (default: ${def})` : '';
  return new Promise((resolve) => {
    rl.question(`${question}${suffix}: `, (ans) => {
      const v = String(ans || '').trim();
      resolve(v || def);
    });
  });
}

function parseList(input) {
  const raw = String(input || '').trim();
  if (!raw) return [];
  return raw
    .split(/[,\n]+/g)
    .map((s) => s.trim())
    .filter(Boolean);
}

function setIfEmpty(obj, key, value) {
  if (obj[key] === undefined || obj[key] === null || obj[key] === '') obj[key] = value;
}

function ensureObjectPath(root, parts) {
  let cur = root;
  for (const p of parts) {
    if (!cur[p] || typeof cur[p] !== 'object') cur[p] = {};
    cur = cur[p];
  }
  return cur;
}

async function main() {
  const root = repoRoot();

  const feishuConfigDir = path.join(root, 'config', 'feishu');
  const secretsDir = path.join(root, 'config', 'secrets');
  const defaultExample = path.join(feishuConfigDir, 'default.example.json');
  const localSecretsExample = path.join(secretsDir, 'local.example.yaml');
  const localSecretsPath = path.join(secretsDir, 'local.yaml');

  if (!fs.existsSync(defaultExample)) {
    throw new Error(`missing template: ${defaultExample}`);
  }

  ensureDir(feishuConfigDir);
  ensureDir(secretsDir);

  const args = new Set(process.argv.slice(2));
  const useDefaults = args.has('--yes') || args.has('-y');

  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
    console.log('SunCodexClaw Docker config setup');
    console.log('note=deprecated; prefer: npm run go:configure');
    console.log(`repo=${root}`);
    console.log('');

    const account = useDefaults ? 'assistant' : await ask(rl, 'Feishu account name (config/feishu/<account>.json)', 'assistant');
    const workspaceInContainer = useDefaults ? '/workspace' : await ask(rl, 'Codex workspace path inside container (mount target)', '/workspace');
    const addDirsRaw = useDefaults ? '' : await ask(rl, 'Additional accessible dirs inside container (comma/newline separated)', '');
    const codexSandbox = useDefaults ? 'workspace-write' : await ask(rl, 'Codex sandbox (workspace-write / danger-full-access / read-only)', 'workspace-write');
    const codexApproval = useDefaults ? 'never' : await ask(rl, 'Codex approval policy (never / on-request / on-failure / untrusted)', 'never');
    const codexBaseUrl = useDefaults ? '' : await ask(rl, 'Codex base url (optional)', '');
    const botName = useDefaults ? '飞书 Codex 助手' : await ask(rl, 'Bot name (recommended to set explicitly)', '飞书 Codex 助手');
    const replyPrefix = useDefaults ? 'AI 助手：' : await ask(rl, 'Reply prefix', 'AI 助手：');
    const progressTitlePrefix = useDefaults ? 'AI 助手｜任务进度' : await ask(rl, 'Progress doc title prefix', 'AI 助手｜任务进度');

    // 1) Ensure local secrets file exists (copy template if needed)
    const createdLocalSecrets = fs.existsSync(localSecretsExample)
      ? copyIfMissing(localSecretsExample, localSecretsPath)
      : false;

    // 2) Merge recommended fields into local.yaml under config.feishu.<account> (without overwriting existing values)
    if (YAML) {
      const localYaml = readYamlIfExists(localSecretsPath) || {};
      const accountSecrets = ensureObjectPath(localYaml, ['config', 'feishu', account]);
      setIfEmpty(accountSecrets, 'bot_name', botName);
      setIfEmpty(accountSecrets, 'reply_mode', 'codex');
      setIfEmpty(accountSecrets, 'reply_prefix', replyPrefix);
      if (accountSecrets.require_mention === undefined) accountSecrets.require_mention = true;
      if (accountSecrets.require_mention_group_only === undefined) accountSecrets.require_mention_group_only = true;
      accountSecrets.progress = accountSecrets.progress && typeof accountSecrets.progress === 'object' ? accountSecrets.progress : {};
      if (accountSecrets.progress.enabled === undefined) accountSecrets.progress.enabled = true;
      setIfEmpty(accountSecrets.progress, 'mode', 'doc');
      accountSecrets.progress.doc = accountSecrets.progress.doc && typeof accountSecrets.progress.doc === 'object' ? accountSecrets.progress.doc : {};
      setIfEmpty(accountSecrets.progress.doc, 'title_prefix', progressTitlePrefix);
      if (accountSecrets.progress.doc.share_to_chat === undefined) accountSecrets.progress.doc.share_to_chat = true;
      setIfEmpty(accountSecrets.progress.doc, 'link_scope', 'same_tenant');
      if (accountSecrets.progress.doc.include_user_message === undefined) accountSecrets.progress.doc.include_user_message = true;
      if (accountSecrets.progress.doc.write_final_reply === undefined) accountSecrets.progress.doc.write_final_reply = true;

      accountSecrets.codex = accountSecrets.codex && typeof accountSecrets.codex === 'object' ? accountSecrets.codex : {};
      setIfEmpty(accountSecrets.codex, 'bin', 'codex');
      if (codexBaseUrl) setIfEmpty(accountSecrets.codex, 'base_url', codexBaseUrl);
      // Sensitive fields: leave empty placeholders if not present
      if (accountSecrets.codex.api_key === undefined) accountSecrets.codex.api_key = '';
      if (accountSecrets.codex.model === undefined) accountSecrets.codex.model = '';
      if (accountSecrets.codex.reasoning_effort === undefined) accountSecrets.codex.reasoning_effort = '';
      if (accountSecrets.codex.profile === undefined) accountSecrets.codex.profile = '';
      setIfEmpty(accountSecrets.codex, 'cwd', workspaceInContainer);
      if (!Array.isArray(accountSecrets.codex.add_dirs)) accountSecrets.codex.add_dirs = [];
      if (accountSecrets.codex.add_dirs.length === 0) accountSecrets.codex.add_dirs = parseList(addDirsRaw);
      if (accountSecrets.codex.history_turns === undefined) accountSecrets.codex.history_turns = 6;
      setIfEmpty(accountSecrets.codex, 'sandbox', codexSandbox);
      setIfEmpty(accountSecrets.codex, 'approval_policy', codexApproval);

      writeYaml(localSecretsPath, localYaml);
    }

    // 3) Write non-sensitive per-account overrides json (minimal)
    const overlay = {
      bot_name: botName,
      reply_mode: 'codex',
      reply_prefix: replyPrefix,
      require_mention: true,
      require_mention_group_only: true,
      progress: {
        enabled: true,
        mode: 'doc',
        doc: {
          title_prefix: progressTitlePrefix,
        },
      },
      codex: {
        cwd: workspaceInContainer,
        add_dirs: parseList(addDirsRaw),
      },
    };
    const outPath = path.join(feishuConfigDir, `${account}.json`);
    writeJson(outPath, overlay);

    console.log('');
    console.log('Done.');
    console.log(`wrote=${outPath}`);
    if (YAML) {
      console.log(`updated=${localSecretsPath}`);
    } else {
      console.log(`note=local.yaml merge skipped (missing dependency "yaml"); run: npm install`);
      console.log(`ensure=${localSecretsPath}`);
    }
    if (createdLocalSecrets) console.log('created=config/secrets/local.yaml (from template)');
    console.log('');
    console.log('Suggested docker run (mount your host workspace to the same container path you set above):');
    console.log(`  docker run --rm \\`);
    console.log(`    -v "$PWD/.codex:/home/node/.codex" \\`);
    console.log(`    -v "$PWD/config:/app/config" \\`);
    console.log(`    -v "<HOST_WORKSPACE>:${workspaceInContainer}" \\`);
    for (const d of parseList(addDirsRaw)) {
      console.log(`    -v "<HOST_DIR>:${d}" \\`);
    }
    console.log(`    ghcr.io/<owner>/<repo>:main`);
  } finally {
    rl.close();
  }
}

main().catch((err) => {
  console.error(`[error] ${err.message}`);
  process.exit(1);
});
