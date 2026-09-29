/** 极简事件总线：解耦 http 拦截器与 store，避免循环依赖。 */
type Handler = (payload?: unknown) => void

const channels = new Map<string, Set<Handler>>()

export const bus = {
  on(name: string, fn: Handler) {
    if (!channels.has(name)) channels.set(name, new Set())
    channels.get(name)!.add(fn)
    return () => bus.off(name, fn)
  },
  off(name: string, fn: Handler) {
    channels.get(name)?.delete(fn)
  },
  emit(name: string, payload?: unknown) {
    channels.get(name)?.forEach((fn) => {
      try {
        fn(payload)
      } catch (e) {
        // 单个订阅者异常不影响其它订阅者
        console.error('[bus]', name, e)
      }
    })
  },
}

export const EVT = {
  /** 任一受保护接口返回 401：展示登录门 */
  UNAUTHORIZED: 'http:401',
  /** 服务端 5xx：顶栏红条，不重复拉取 */
  SERVER_ERROR: 'http:5xx',
  /** 网络层失败（后端未启动 / 断连） */
  NETWORK_ERROR: 'http:network',
} as const
