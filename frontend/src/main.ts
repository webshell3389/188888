import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import * as ElIcons from '@element-plus/icons-vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import 'element-plus/dist/index.css'

import App from './App.vue'
import { router } from './router'
import './styles/tokens.css'
import './styles/global.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(ElementPlus, { locale: zhCn })

// 图标全量注册（侧栏/按钮按名取用，避免逐处 import）
for (const [name, comp] of Object.entries(ElIcons)) {
  app.component(name, comp as never)
}

app.mount('#app')
