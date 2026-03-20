const fs = require('fs');
const path = require('path');

function resolveBotOverlayPath(configDir) {
  return path.resolve(configDir, 'feishu', 'bots.toml');
}

function asPlainObject(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return value;
}

function stripComment(line) {
  let out = '';
  let inSingle = false;
  let inDouble = false;
  let escaped = false;
  for (let i = 0; i < line.length; i += 1) {
    const ch = line[i];
    if (escaped) {
      out += ch;
      escaped = false;
      continue;
    }
    if (ch === '\\' && inDouble) {
      out += ch;
      escaped = true;
      continue;
    }
    if (ch === "'" && !inDouble) {
      inSingle = !inSingle;
      out += ch;
      continue;
    }
    if (ch === '"' && !inSingle) {
      inDouble = !inDouble;
      out += ch;
      continue;
    }
    if (ch === '#' && !inSingle && !inDouble) break;
    out += ch;
  }
  return out;
}

function findSeparator(line, separator) {
  let inSingle = false;
  let inDouble = false;
  let escaped = false;
  let bracketDepth = 0;
  for (let i = 0; i < line.length; i += 1) {
    const ch = line[i];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (ch === '\\' && inDouble) {
      escaped = true;
      continue;
    }
    if (ch === "'" && !inDouble) {
      inSingle = !inSingle;
      continue;
    }
    if (ch === '"' && !inSingle) {
      inDouble = !inDouble;
      continue;
    }
    if (inSingle || inDouble) continue;
    if (ch === '[') {
      bracketDepth += 1;
      continue;
    }
    if (ch === ']' && bracketDepth > 0) {
      bracketDepth -= 1;
      continue;
    }
    if (bracketDepth === 0 && ch === separator) return i;
  }
  return -1;
}

function parseQuotedString(raw, lineNo) {
  if (raw.startsWith('"')) {
    try {
      return JSON.parse(raw);
    } catch (err) {
      throw new Error(`invalid double-quoted string at line ${lineNo}: ${err.message}`);
    }
  }
  if (raw.startsWith("'")) {
    if (!raw.endsWith("'") || raw.length < 2) {
      throw new Error(`invalid single-quoted string at line ${lineNo}`);
    }
    return raw.slice(1, -1);
  }
  throw new Error(`invalid quoted string at line ${lineNo}`);
}

function parseValue(raw, lineNo) {
  const value = String(raw || '').trim();
  if (!value) return '';
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    return parseQuotedString(value, lineNo);
  }
  if (value.startsWith('[') && value.endsWith(']')) {
    return parseArray(value.slice(1, -1), lineNo);
  }
  if (/^(true|false)$/i.test(value)) {
    return value.toLowerCase() === 'true';
  }
  if (/^[+-]?\d+$/.test(value)) {
    return Number.parseInt(value, 10);
  }
  return value;
}

function parseArray(raw, lineNo) {
  const inner = String(raw || '').trim();
  if (!inner) return [];
  const items = [];
  let start = 0;
  while (start < inner.length) {
    const comma = findSeparator(inner.slice(start), ',');
    if (comma === -1) {
      items.push(inner.slice(start).trim());
      break;
    }
    items.push(inner.slice(start, start + comma).trim());
    start += comma + 1;
  }
  return items.filter(Boolean).map((item) => parseValue(item, lineNo));
}

function parseTablePart(raw, lineNo) {
  if (!raw) throw new Error(`empty table path at line ${lineNo}`);
  if ((raw.startsWith('"') && raw.endsWith('"')) || (raw.startsWith("'") && raw.endsWith("'"))) {
    return parseQuotedString(raw, lineNo);
  }
  return raw;
}

function parseTablePath(raw, lineNo) {
  const inner = String(raw || '').trim();
  if (!inner) throw new Error(`empty table header at line ${lineNo}`);
  const parts = [];
  let buf = '';
  let inSingle = false;
  let inDouble = false;
  let escaped = false;
  for (let i = 0; i < inner.length; i += 1) {
    const ch = inner[i];
    if (escaped) {
      buf += ch;
      escaped = false;
      continue;
    }
    if (ch === '\\' && inDouble) {
      buf += ch;
      escaped = true;
      continue;
    }
    if (ch === "'" && !inDouble) {
      inSingle = !inSingle;
      buf += ch;
      continue;
    }
    if (ch === '"' && !inSingle) {
      inDouble = !inDouble;
      buf += ch;
      continue;
    }
    if (ch === '.' && !inSingle && !inDouble) {
      parts.push(parseTablePart(buf.trim(), lineNo));
      buf = '';
      continue;
    }
    buf += ch;
  }
  if (inSingle || inDouble) {
    throw new Error(`unterminated table header at line ${lineNo}`);
  }
  parts.push(parseTablePart(buf.trim(), lineNo));
  return parts;
}

function ensureObjectPath(root, parts) {
  let cur = root;
  for (const part of parts) {
    if (!cur[part] || typeof cur[part] !== 'object' || Array.isArray(cur[part])) {
      cur[part] = {};
    }
    cur = cur[part];
  }
  return cur;
}

function parseBotOverlayToml(raw) {
  const root = {};
  let current = [];
  const lines = String(raw || '').split(/\r?\n/);
  for (let i = 0; i < lines.length; i += 1) {
    const lineNo = i + 1;
    const line = stripComment(lines[i]).trim();
    if (!line) continue;
    if (line.startsWith('[')) {
      if (!line.endsWith(']')) throw new Error(`unterminated table header at line ${lineNo}`);
      current = parseTablePath(line.slice(1, -1), lineNo);
      ensureObjectPath(root, current);
      continue;
    }
    const eq = findSeparator(line, '=');
    if (eq <= 0) throw new Error(`invalid assignment at line ${lineNo}`);
    const key = line.slice(0, eq).trim();
    if (!key) throw new Error(`empty key at line ${lineNo}`);
    ensureObjectPath(root, current)[key] = parseValue(line.slice(eq + 1), lineNo);
  }
  return root;
}

function readBotOverlay(configDir) {
  const filePath = resolveBotOverlayPath(configDir);
  if (!fs.existsSync(filePath)) {
    return { path: filePath, shared: {}, bots: {}, exists: false };
  }
  const root = asPlainObject(parseBotOverlayToml(fs.readFileSync(filePath, 'utf8')));
  return {
    path: filePath,
    shared: asPlainObject(root.shared),
    bots: asPlainObject(root.bot),
    exists: true,
  };
}

module.exports = {
  readBotOverlay,
  resolveBotOverlayPath,
};
