const fs = require('fs');
const path = require('path');
const YAML = require('yaml');

const DEFAULT_SECRETS_FILE = path.resolve(__dirname, '..', '..', 'config', 'secrets', 'local.yaml');

let cachedFilePath = '';
let cachedMtimeMs = -1;
let cachedDoc = null;

function resolveSecretsFile() {
  const explicit = String(
    process.env.SUNCODEXCLAW_SECRET_YAML
    || process.env.SUNCODEXCLAW_SECRETS_FILE
    || process.env.CODEX_CLAW_SECRET_YAML
    || process.env.CODEX_CLAW_SECRETS_FILE
    || ''
  ).trim();
  return explicit ? path.resolve(explicit) : DEFAULT_SECRETS_FILE;
}

function loadLocalSecrets() {
  const filePath = resolveSecretsFile();
  if (!fs.existsSync(filePath)) {
    cachedFilePath = filePath;
    cachedMtimeMs = -1;
    cachedDoc = null;
    return { filePath, doc: null };
  }

  const stat = fs.statSync(filePath);
  if (cachedFilePath === filePath && cachedMtimeMs === stat.mtimeMs) {
    return { filePath, doc: cachedDoc };
  }

  const raw = fs.readFileSync(filePath, 'utf8');
  const parsed = YAML.parse(raw) || {};
  if (parsed && typeof parsed !== 'object') {
    throw new Error(`invalid yaml in ${filePath}: expected mapping at root`);
  }

  cachedFilePath = filePath;
  cachedMtimeMs = stat.mtimeMs;
  cachedDoc = parsed;
  return { filePath, doc: parsed };
}

function asPlainObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return value;
}

function cloneSerializable(value) {
  if (value === undefined) return undefined;
  return JSON.parse(JSON.stringify(value));
}

function deepMerge(...items) {
  const out = {};
  for (const item of items) {
    const src = asPlainObject(item);
    for (const [key, value] of Object.entries(src)) {
      if (Array.isArray(value)) {
        out[key] = value.slice();
        continue;
      }
      if (value && typeof value === 'object') {
        out[key] = deepMerge(asPlainObject(out[key]), value);
        continue;
      }
      out[key] = value;
    }
  }
  return out;
}

function readConfigRoot(section, fallback = undefined) {
  const key = String(section || '').trim();
  if (!key) return fallback;
  const { doc } = loadLocalSecrets();
  const configRoot = asPlainObject(doc?.config);
  const fromConfig = asPlainObject(configRoot[key]);
  if (Object.keys(fromConfig).length > 0) return fromConfig;

  // Backward compatibility for earlier local.yaml layouts.
  const legacyValues = asPlainObject(doc?.values);
  const fromLegacy = asPlainObject(legacyValues[key]);
  if (Object.keys(fromLegacy).length > 0) return fromLegacy;
  return fallback;
}

function readConfigEntry(section, name = 'default', fallback = undefined) {
  const root = asPlainObject(readConfigRoot(section, {}));
  const key = String(name || '').trim();
  if (!key) return fallback;
  const entry = asPlainObject(root[key]);
  if (Object.keys(entry).length > 0) return entry;
  return fallback;
}

function listConfigEntryNames(section) {
  const root = asPlainObject(readConfigRoot(section, {}));
  return Object.keys(root).sort();
}

function normalizeSecretsDoc(doc) {
  const nextDoc = asPlainObject(cloneSerializable(doc) || {});
  nextDoc.config = asPlainObject(nextDoc.config);
  return nextDoc;
}

function writeLocalSecrets(doc) {
  const filePath = resolveSecretsFile();
  const nextDoc = normalizeSecretsDoc(doc);
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  const body = YAML.stringify(nextDoc).trimEnd();
  fs.writeFileSync(filePath, `${body}\n`, 'utf8');

  const stat = fs.statSync(filePath);
  cachedFilePath = filePath;
  cachedMtimeMs = stat.mtimeMs;
  cachedDoc = nextDoc;
  return filePath;
}

function upsertConfigEntry(section, name, value, options = {}) {
  const sectionName = String(section || '').trim();
  const entryName = String(name || '').trim();
  if (!sectionName) throw new Error('section is required');
  if (!entryName) throw new Error('name is required');

  const merge = options.merge !== false;
  const { doc } = loadLocalSecrets();
  const nextDoc = normalizeSecretsDoc(doc);
  const configRoot = asPlainObject(nextDoc.config);
  const sectionRoot = asPlainObject(configRoot[sectionName]);
  const currentValue = asPlainObject(sectionRoot[entryName]);
  const incomingValue = asPlainObject(cloneSerializable(value) || {});
  sectionRoot[entryName] = merge
    ? deepMerge(currentValue, incomingValue)
    : incomingValue;
  configRoot[sectionName] = sectionRoot;
  nextDoc.config = configRoot;

  return {
    filePath: writeLocalSecrets(nextDoc),
    value: sectionRoot[entryName],
  };
}

module.exports = {
  DEFAULT_SECRETS_FILE,
  asPlainObject,
  deepMerge,
  listConfigEntryNames,
  readConfigEntry,
  readConfigRoot,
  loadLocalSecrets,
  resolveSecretsFile,
  upsertConfigEntry,
  writeLocalSecrets,
};
