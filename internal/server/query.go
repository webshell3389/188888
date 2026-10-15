package server

import (
	"net/http"

	"mockc2/internal/store"
)

// 只读查询面：/__q —— DB 元信息库的只读 REST 视图。
//
// 收敛后仅保留 Vue 控制台「设备控制台」依赖的三个端点（按设备独立指令队列）。
// 其余 /__q/* 历史视图（devices / timeline / packets / packet / meta / stats /
// wallets / wallets_rules / wallet_flags / apps / keychain_items / keychain_devices /
// memos / result_data / profiles / full_stats）已随前端全量迁移 Vue 下线：
// 对应数据由 /__dash/api、/panel/api/*、/api/data/* 单一来源承载，不再重复暴露。

func qLimit(r *http.Request) int {
	n := queryInt(r, "limit", 500)
	if n <= 0 {
		return 500
	}
	if n > 2000 {
		return 2000
	}
	return n
}

func registerQuery(rt *Router) {
	// 指令历史（全局 + 设备维度）
	rt.GET("/__q/commands", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.CommandRecent(qLimit(r)))
	})

	// 按设备独立指令队列：总览 + 明细（前端设备控制台）
	rt.GET("/__q/device_command_targets", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.GetQueue().ListTargets())
	})

	rt.GET("/__q/device_commands", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		target := r.URL.Query().Get("target")
		if target == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target required"})
			return
		}
		writeJSON(w, http.StatusOK, store.GetQueue().ListByTarget(target, qLimit(r)))
	})
}
