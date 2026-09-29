/** 展示层格式化：全部纯字符串处理，不做时区换算（契约 §4.5：ts 为后端 UTC 字符串，前端不得偏移）。 */

export function fmtBytes(n?: number | null): string {
  if (n == null || Number.isNaN(n)) return '-'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 2 : 1)} ${units[i]}`
}

export function clip(s: unknown, n = 120): string {
  const str = String(s ?? '')
  return str.length > n ? str.slice(0, n) + '…' : str
}

/** IP 缩短显示：IPv6 压缩（保留前 3 组 + …），IPv4 原样返回 */
export function shortIP(ip?: string): string {
  if (!ip) return '-'
  if (ip.includes(':')) {
    const groups = ip.split(':').filter(Boolean)
    if (groups.length > 4) {
      return groups.slice(0, 3).join(':') + ':…'
    }
  }
  return ip
}

export function fmtInt(n: unknown): string {
  const v = Number(n ?? 0)
  if (!Number.isFinite(v)) return '-'
  return v.toLocaleString('en-US')
}

/** HTTP 状态语义：≥500 红 / ≥400 黄 / 其它绿；/p 的 404 是预期值（照片回传已禁用） */
export function statusLevel(status?: number, path = ''): 'ok' | 'warn' | 'err' | 'muted' {
  if (status == null) return 'muted'
  if (status >= 500) return 'err'
  if (status >= 400) return isExpected404(status, path) ? 'muted' : 'warn'
  return 'ok'
}

export function isExpected404(status: number, path = ''): boolean {
  return status === 404 && (path === '/p' || path.startsWith('/p/'))
}

/** 设备复合去重键 (ip, ua, deviceVersion) —— 契约 §6 */
export function deviceKey(d: { ip?: string; ua?: string; deviceVersion?: string }): string {
  return `${d.ip || ''}|${d.ua || ''}|${d.deviceVersion || ''}`
}

export function dedupeBy<T>(list: T[], keyFn: (x: T) => string): T[] {
  const seen = new Set<string>()
  const out: T[] = []
  for (const x of list) {
    const k = keyFn(x)
    if (seen.has(k)) continue
    seen.add(k)
    out.push(x)
  }
  return out
}

/** UA 摘要：把长 UA 压成「iPhone 18.6.2 / Chrome 153 / Python」这类短标签 */
export function uaLabel(ua?: string): string {
  if (!ua) return '-'
  const s = ua
  if (/iPhone/.test(s)) {
    const m = s.match(/iPhone OS (\d+)[._](\d+)(?:[._](\d+))?/)
    return m ? `iPhone ${m[1]}.${m[2]}${m[3] ? '.' + m[3] : ''}` : 'iPhone'
  }
  if (/iPad/.test(s)) return 'iPad'
  if (/Android/.test(s)) return 'Android'
  if (/Macintosh/.test(s)) return 'macOS'
  if (/Windows NT/.test(s)) return 'Windows'
  if (/Python-urllib/.test(s)) return 'python'
  if (/curl/i.test(s)) return 'curl'
  if (/Go-http-client/.test(s)) return 'go-http'
  return clip(s, 24)
}

/** ts 形如 "2026-09-24 03:11:10"，直接切片（不构造 Date，避免时区偏移） */
export function tsDate(ts?: string): string {
  return ts ? ts.slice(0, 10) : '-'
}
export function tsTime(ts?: string): string {
  return ts && ts.length >= 19 ? ts.slice(11, 19) : ts || '-'
}
export function tsShort(ts?: string): string {
  return ts && ts.length >= 19 ? ts.slice(5, 19) : ts || '-'
}

/** 尝试美化 JSON 字符串；失败原样返回（binary / 截断体） */
export function prettyJson(text: string): string {
  const t = (text ?? '').trim()
  if (!t) return '(empty)'
  try {
    return JSON.stringify(JSON.parse(t), null, 2)
  } catch {
    return t
  }
}

export function pctClass(pct?: number): string {
  if (pct == null) return ''
  if (pct >= 100) return 'bar__fill--ok'
  if (pct >= 46) return 'bar__fill--warn'
  return ''
}
