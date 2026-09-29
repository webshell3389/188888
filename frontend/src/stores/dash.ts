import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { dashApi } from '@/api/dash'
import type { AccessLog, DashSnapshot, Device, PacketItem, PeEvent, ProbeLog, WalletStat } from '@/api/types'
import { dedupeBy, deviceKey } from '@/utils/format'
import { bus, EVT } from '@/utils/bus'
import { useUiStore } from './ui'

/**
 * 单一数据源（契约 §4.1）：一切实时视图只消费这里的一份 /__dash/api 快照。
 *
 * 轮询纪律：
 *  - 单飞：上一请求未落地不发下一个（杜绝"重复调用"导致的行闪烁与队列堆积）
 *  - 页面不可见时暂停，恢复可见立即补一次
 *  - 5xx / 网络失败 → 顶栏红条，指数退避，不疯狂重试
 */
export const useDashStore = defineStore('dash', () => {
  const ui = useUiStore()

  const snap = ref<DashSnapshot>({
    ts: '',
    devices: [],
    pe_events: [],
    wallets: [],
    log: [],
    access: [],
    endpoints: {},
    counts: {},
  })

  const loading = ref(false)
  const lastOkAt = ref(0)
  const ready = ref(false)

  let timer: number | undefined
  let inFlight = false
  let backoff = 1

  const devices = computed<Device[]>(() => dedupeBy(snap.value.devices || [], deviceKey))
  const peEvents = computed<PeEvent[]>(() => snap.value.pe_events || [])
  const wallets = computed<WalletStat[]>(() => snap.value.wallets || [])
  const logs = computed<ProbeLog[]>(() => snap.value.log || [])
  const access = computed<AccessLog[]>(() => snap.value.access || [])
  const endpoints = computed<Record<string, PacketItem[]>>(() => snap.value.endpoints || {})
  const counts = computed<Record<string, number>>(() => snap.value.counts || {})

  /** 端点分组：key 完全动态（capture/ 下出现新目录即自动成组），前端不写死列表 */
  const endpointKeys = computed<string[]>(() => {
    const keys = new Set<string>([...Object.keys(endpoints.value), ...Object.keys(counts.value)])
    return [...keys].sort()
  })

  const kpi = computed(() => {
    const c = counts.value
    const totalPackets = Object.values(c).reduce((a, b) => a + (Number(b) || 0), 0)
    return {
      devices: devices.value.length,
      pe: peEvents.value.length,
      packets: totalPackets,
      beacon: Number(c.beacon || 0),
      war: Number(c.war || 0),
      result: Number(c.result || 0),
      wallets: wallets.value.length,
      access: access.value.length,
    }
  })

  async function refresh(): Promise<void> {
    if (inFlight) return
    inFlight = true
    loading.value = true
    try {
      const data = await dashApi.snapshot()
      if (data && typeof data === 'object') {
        snap.value = {
          ts: data.ts || '',
          devices: data.devices || [],
          pe_events: data.pe_events || [],
          wallets: data.wallets || [],
          log: data.log || [],
          access: data.access || [],
          endpoints: data.endpoints || {},
          counts: data.counts || {},
          progress: data.progress,
        }
        lastOkAt.value = Date.now()
        ready.value = true
        backoff = 1
        ui.clearError()
      }
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } })?.response?.status
      if (!status) {
        // 网络层：后端未启动 / 断连
        ui.setError('network', '无法连接后端（检查 9001 端口是否存活）')
      } else if (status >= 500) {
        ui.setError('server', `后端返回 ${status}，已暂停高频重试`)
        backoff = Math.min(backoff * 2, 8)
      }
    } finally {
      inFlight = false
      loading.value = false
    }
  }

  function scheduleNext() {
    if (timer) window.clearTimeout(timer)
    if (!ui.autoRefresh) return
    timer = window.setTimeout(async () => {
      if (document.hidden) {
        scheduleNext()
        return
      }
      await refresh()
      scheduleNext()
    }, ui.refreshMs * backoff)
  }

  function start() {
    if (!ui.autoRefresh) {
      void refresh()
      return
    }
    void refresh()
    scheduleNext()
  }

  function stop() {
    if (timer) window.clearTimeout(timer)
    timer = undefined
  }

  /** 用户改轮询节奏 / 开关 → 立即重排 */
  watch([() => ui.refreshMs, () => ui.autoRefresh], () => {
    scheduleNext()
  })

  // 页面重新可见 → 立即补一次（不等待下一个 tick）
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden && ui.autoRefresh) void refresh()
    })
  }

  bus.on(EVT.UNAUTHORIZED, () => stop())
  bus.on(EVT.SERVER_ERROR, () => {
    backoff = Math.min(backoff * 2, 8)
  })

  return {
    snap,
    loading,
    ready,
    lastOkAt,
    devices,
    peEvents,
    wallets,
    logs,
    access,
    endpoints,
    counts,
    endpointKeys,
    kpi,
    refresh,
    start,
    stop,
  }
})
