const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');
const YAML = require('yaml');

const DEFAULT_SECRETS_FILE = path.resolve(__dirname, '..', '..', 'config', 'secrets', 'local.yaml');
const DEFAULT_KEYCHAIN_ACCOUNT = 'codex-claw';

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

function resolveKeychainAccount() {
  return String(
    process.env.SUNCODEXCLAW_KEYCHAIN_ACCOUNT
    || process.env.CODEX_CLAW_KEYCHAIN_ACCOUNT
    || DEFAULT_KEYCHAIN_ACCOUNT
  ).trim() || DEFAULT_KEYCHAIN_ACCOUNT;
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

function normalizeSecretString(value) {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value.trim();
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return '';
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

function readKeychainSecret(service) {
  const normalized = String(service || '').trim();
  if (!normalized) return '';
  const account = resolveKeychainAccount();
  try {
    return execSync(`security find-generic-password -a "${account}" -s "${normalized}" -w`, {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
  } catch (_) {
    return '';
  }
}

function readYamlServiceSecret(service) {
  const normalized = String(service || '').trim();
  if (!normalized) return '';
  const { doc } = loadLocalSecrets();
  const services = doc?.services;
  if (!services || typeof services !== 'object') return '';
  return normalizeSecretString(services[normalized]);
}

function readServiceSecret(service) {
  return readYamlServiceSecret(service) || readKeychainSecret(service);
}

function readLocalValue(pathSegments, fallback = undefined) {
  const parts = Array.isArray(pathSegments) ? pathSegments : [pathSegments];
  const { doc } = loadLocalSecrets();
  let current = doc?.values;
  for (const part of parts) {
    const key = String(part || '').trim();
    if (!key || !current || typeof current !== 'object' || !(key in current)) {
      return fallback;
    }
    current = current[key];
  }
  return current === undefined ? fallback : current;
}

function readConfigRoot(section, fallback = undefined) {
  const key = String(section || '').trim();
  if (!key) return fallback;
  const { doc } = loadLocalSecrets();
  const configRoot = asPlainObject(doc?.config);
  const fromConfig = asPlainObject(configRoot[key]);
  if (Object.keys(fromConfig).length > 0) return fromConfig;

  // Backward compatibility for the earlier secrets-only layout.
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
  nextDoc.services = asPlainObject(nextDoc.services);
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
  readKeychainSecret,
  readLocalValue,
  readServiceSecret,
  readYamlServiceSecret,
  resolveSecretsFile,
  upsertConfigEntry,
  writeLocalSecrets,
};
