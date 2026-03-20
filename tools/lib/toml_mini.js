const fs = require('fs');

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

function parseTablePart(raw, lineNo) {
  const trimmed = String(raw || '').trim();
  if (!trimmed) throw new Error(`empty table path at line ${lineNo}`);
  if ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'"))) {
    return parseQuotedString(trimmed, lineNo);
  }
  return trimmed;
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
      parts.push(parseTablePart(buf, lineNo));
      buf = '';
      continue;
    }
    buf += ch;
  }
  if (inSingle || inDouble) throw new Error(`unterminated table header at line ${lineNo}`);
  parts.push(parseTablePart(buf, lineNo));
  return parts;
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
  if (/^(true|false)$/i.test(value)) return value.toLowerCase() === 'true';
  if (/^[+-]?\d+$/.test(value)) return Number.parseInt(value, 10);
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

function parseToml(raw) {
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

function quoteString(value) {
  return JSON.stringify(String(value));
}

function isBareKey(value) {
  const raw = String(value || '');
  if (!raw) return false;
  return /^[A-Za-z0-9_-]+$/.test(raw);
}

function formatPath(parts) {
  return parts.map((part) => isBareKey(part) ? part : quoteString(part)).join('.');
}

function formatValue(value) {
  if (Array.isArray(value)) {
    return `[${value.map((item) => formatValue(item)).join(', ')}]`;
  }
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  if (typeof value === 'number' && Number.isInteger(value)) return String(value);
  return quoteString(value == null ? '' : value);
}

function writeTable(lines, obj, prefix) {
  const map = asPlainObject(obj);
  const scalarKeys = [];
  const childKeys = [];
  for (const key of Object.keys(map)) {
    const value = map[key];
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      childKeys.push(key);
      continue;
    }
    scalarKeys.push(key);
  }
  if (prefix.length > 0 && scalarKeys.length > 0) {
    lines.push(`[${formatPath(prefix)}]`);
    for (const key of scalarKeys) {
      lines.push(`${key} = ${formatValue(map[key])}`);
    }
    lines.push('');
  }
  for (const key of childKeys) {
    writeTable(lines, map[key], prefix.concat(key));
  }
}

function stringifyToml(obj) {
  const root = asPlainObject(obj);
  const lines = [];
  writeTable(lines, root, []);
  return `${lines.join('\n').replace(/\n{3,}/g, '\n\n').trimEnd()}\n`;
}

function readTomlIfExists(filePath) {
  if (!fs.existsSync(filePath)) return null;
  return parseToml(fs.readFileSync(filePath, 'utf8'));
}

module.exports = {
  asPlainObject,
  ensureObjectPath,
  parseToml,
  readTomlIfExists,
  stringifyToml,
};
