import type { DashSnapshot } from './types'

/**
 * 离线快照（VITE_USE_MOCK=1）。
 * 仅用于后端未启动时的 UI 联调：结构与真实 /__dash/api 一致，体量极小（不含大回传正文）。
 */
export function buildMockSnapshot(): DashSnapshot {
  const ts = '2026-09-24 03:11:10'
  const ua =
    'Mozilla/5.0 (iPhone; CPU iPhone OS 18_6_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1'
  return {
    ts,
    devices: [
      { ts, channelCode: '1', deviceVersion: 'IOS 18.6.2', domain: 'lab', ip: '127.0.0.1', ua, host: '127.0.0.1:9001' },
      { ts, channelCode: '5', deviceVersion: 'IOS 18.5', domain: 'lab.t', ip: '10.0.0.7', ua, host: 'lab.t:9001' },
    ],
    pe_events: [{ ts, ip: '127.0.0.1', ua, host: '127.0.0.1:9001', detail: '' }],
    wallets: [{ rule: 'trust', display: 'Trust Wallet', count: 3, last_ts: ts }],
    log: [
      { ts, channel: '-', id: '111', hex: '0', text: '[2765ms]  [sbx0] stage1: init WebProcess discovery' },
      { ts, channel: '-', id: '112', hex: '0', text: '[2776ms]  [sbx0] stage2: SBX0() enter, retry=0' },
    ],
    access: [
      { ts, role: 'delivery', method: 'GET', path: '/boot.html', query: 'c=1', host: '127.0.0.1:9001', client: '127.0.0.1', ua, status: 200, cost_ms: 1.63 },
      { ts, role: 'c2', method: 'POST', path: '/beacon', query: '', host: '127.0.0.1:9001', client: '127.0.0.1', ua, status: 200, cost_ms: 7.27 },
    ],
    endpoints: {
      sync: [{ ts, file: '20260924-031110-6a9bd699.json', size: 68, body: '{"channelCode":"1","deviceVersion":"IOS 18.6.2"}', parsed: { kind: 'sync', channelCode: '1', deviceVersion: 'IOS 18.6.2', domain: 'lab' } }],
      beacon: [{ ts, file: '20260924-031110-5afe3afd.json', size: 51, body: '{"uuid":"U1","status":"idle"}', parsed: { kind: 'beacon', uuid: 'U1', status: 'idle' } }],
      war: [{ ts: '-', file: '20260924-023138-597cc99d.json', size: 587450, body: '{"device_uuid":"02B2D2E3…"}', parsed: { kind: 'war', probe: null } }],
      result: [{ ts, file: '20260924-031110-1a514de6.json', size: 36, body: '{"command_id":"c1","status":"ok"}', parsed: { kind: 'result', command_id: 'c1', status: 'ok' } }],
    },
    counts: { sync: 16, beacon: 89, war: 4, result: 19, event: 43, nb: 15, u: 12, a: 12, ph: 8, heartbeat: 9 },
  }
}
